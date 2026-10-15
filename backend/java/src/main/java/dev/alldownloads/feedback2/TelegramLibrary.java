package dev.alldownloads.feedback2;

import com.fasterxml.jackson.databind.*;
import com.fasterxml.jackson.databind.node.*;
import java.math.BigInteger;
import java.sql.*;
import java.time.*;
import java.time.format.DateTimeFormatter;
import java.util.*;

/** Saved-message business rules only. The caller supplies the existing transactional operation queue. */
public final class TelegramLibrary extends DomainSql {
    private final IssueRepository issues;
    public TelegramLibrary(Connection db,ObjectMapper json,Clock clock,IssueRepository issues) { super(db,json,clock); this.issues=issues; }
    public void initialize() throws SQLException { ddl(
        "CREATE TABLE IF NOT EXISTS f2_tel_accounts(id TEXT PRIMARY KEY,cursor TEXT NOT NULL DEFAULT '0',high_water TEXT NOT NULL DEFAULT '0',initial_done INTEGER NOT NULL DEFAULT 0,enabled INTEGER NOT NULL DEFAULT 0,revision INTEGER NOT NULL DEFAULT 1,last_scan TEXT,active_task TEXT,batch_id TEXT)",
        "CREATE TABLE IF NOT EXISTS f2_tel_batches(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,min_sent TEXT,directory TEXT UNIQUE)",
        "CREATE TABLE IF NOT EXISTS f2_tel_messages(account_id TEXT NOT NULL,external_id TEXT NOT NULL,unit_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(account_id,external_id))",
        "CREATE TABLE IF NOT EXISTS f2_tel_units(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,batch_id TEXT NOT NULL,complete INTEGER NOT NULL DEFAULT 0,eligible INTEGER NOT NULL DEFAULT 0,task_id TEXT,payload TEXT,status TEXT NOT NULL DEFAULT 'PENDING')",
        "CREATE TABLE IF NOT EXISTS f2_tel_pages(event_id TEXT PRIMARY KEY,body TEXT NOT NULL)",
        "CREATE TABLE IF NOT EXISTS f2_tel_files(task_id TEXT NOT NULL,output_id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(task_id,output_id))",
        "CREATE TABLE IF NOT EXISTS f3_tel_task_units(task_id TEXT PRIMARY KEY,unit_id TEXT NOT NULL,outputs TEXT NOT NULL)"); }
    public ObjectNode beginScan(String accountId,String taskId) throws SQLException {
        writing(); require(accountId!=null && !accountId.isBlank(),"INVALID_INPUT");
        exec("INSERT OR IGNORE INTO f2_tel_accounts(id) VALUES(?)",accountId);
        var r=row("SELECT cursor,active_task,batch_id FROM f2_tel_accounts WHERE id=?",accountId);
        if(r[1]!=null) require(r[1].equals(taskId),"SCAN_ACTIVE");
        else { String batch=UUID.randomUUID().toString(); exec("INSERT INTO f2_tel_batches(id,account_id) VALUES(?,?)",batch,accountId);
            exec("UPDATE f2_tel_accounts SET active_task=?,batch_id=?,revision=revision+1 WHERE id=?",taskId,batch,accountId); }
        return obj().put("schemaVersion",1).put("accountId",accountId).put("afterMessageId",r[0]).putNull("continuation");
    }
    public String activeTask(String accountId) throws SQLException { var r=row("SELECT active_task FROM f2_tel_accounts WHERE id=?",accountId); return r==null?null:r[0]; }
    public ObjectNode watcher(String accountId) throws SQLException {
        var r=row("SELECT cursor,initial_done,enabled,revision,last_scan,active_task FROM f2_tel_accounts WHERE id=?",accountId);
        ObjectNode scan=obj().put("accountId",accountId).put("cursorMessageId",r==null?"0":r[0]).put("initialScanCompleted",r!=null && "1".equals(r[1]))
            .put("lastScanAt",r==null?null:r[4]).put("activeTaskId",r==null?null:r[5]).putNull("scannedThroughAt");
        if(r!=null && !r[0].equals("0")) { var m=row("SELECT body FROM f2_tel_messages WHERE account_id=? AND external_id=?",accountId,r[0]); if(m!=null) scan.put("scannedThroughAt",read(m[0]).path("sentAt").asText()); }
        boolean enabled=r!=null && "1".equals(r[2]);
        ObjectNode out=obj().put("accountId",accountId).put("revision",r==null?"0":r[3]).put("enabled",enabled).put("intervalSeconds",60).put("lastCheckedAt",r==null?null:r[4]);
        out.put("nextCheckAt",enabled && r[4]!=null?Instant.parse(r[4]).plusSeconds(60).toString():null); return out.set("scanState",scan);
    }
    public ObjectNode setWatcher(String accountId,boolean enabled,String revision) throws SQLException {
        writing(); ObjectNode old=watcher(accountId); require(old.path("revision").asText().equals(revision),"VERSION_CHANGED");
        if(enabled) require(old.path("scanState").path("initialScanCompleted").asBoolean(),"INITIAL_SCAN_REQUIRED");
        exec("INSERT OR IGNORE INTO f2_tel_accounts(id) VALUES(?)",accountId);
        exec("UPDATE f2_tel_accounts SET enabled=?,revision=revision+1 WHERE id=?",enabled?1:0,accountId); return watcher(accountId);
    }
    public List<String> dueAccounts() throws SQLException {
        List<String> out=new ArrayList<>(); for(var r:rows("SELECT id,last_scan FROM f2_tel_accounts WHERE enabled=1 AND initial_done=1 AND active_task IS NULL"))
            if(r[1]==null || !Instant.parse(r[1]).plusSeconds(60).isAfter(clock.instant())) out.add(r[0]); return out;
    }
    /** Every fact, pending unit, cursor and continuation is committed by the caller in one transaction. */
    public void acceptPage(String taskId,String eventId,ObjectNode page,Enqueue enqueue) throws SQLException {
        writing(); var prior=row("SELECT body FROM f2_tel_pages WHERE event_id=?",eventId);
        if(prior!=null) { require(prior[0].equals(page.toString()),"EVENT_CONFLICT"); return; }
        String account=text(page,"accountId"); var state=row("SELECT cursor,high_water,batch_id,active_task FROM f2_tel_accounts WHERE id=?",account);
        require(state!=null && taskId.equals(state[3]),"SCAN_NOT_ACTIVE");
        boolean finished=page.path("complete").asBoolean(); String continuation=nullable(page,"continuation"); require(finished==(continuation==null),"PROVIDER_CHANGED");
        BigInteger scanned=number(text(page,"scannedThroughMessageId")); BigInteger high=number(state[1]).max(scanned);
        require(page.path("messages").isArray(),"PROVIDER_CHANGED"); Set<String> touched=new LinkedHashSet<>();
        Map<String,Boolean> groupCompleteness=new HashMap<>();
        for(JsonNode message:page.path("messages")) {
            String external=text(message,"externalId"); require(number(external).signum()>0,"PROVIDER_CHANGED");
            String sent=text(message,"sentAt"); Instant instant; try { instant=Instant.parse(sent); } catch(Exception e) { throw new DomainError("PROVIDER_CHANGED"); }
            String group=nullable(message,"groupId"); boolean complete=message.path("groupComplete").asBoolean();
            require(group!=null || complete,"PROVIDER_CHANGED");
            if(group!=null) { Boolean previous=groupCompleteness.putIfAbsent(group,complete); require(previous==null || previous==complete,"PROVIDER_CHANGED"); }
            String unit=stable("tel-",account,group==null?"message:"+external:"group:"+group);
            var known=row("SELECT unit_id,body FROM f2_tel_messages WHERE account_id=? AND external_id=?",account,external);
            require(known==null || unit.equals(known[0]),"PROVIDER_CHANGED");
            validateAttachments(message.path("attachments"));
            exec("INSERT OR IGNORE INTO f2_tel_units(id,account_id,batch_id) VALUES(?,?,?)",unit,account,state[2]);
            // Frozen downloads retain their original fact snapshot; repeats cannot expand an accepted task.
            var frozen=row("SELECT payload FROM f2_tel_units WHERE id=?",unit);
            if(frozen[0]==null) exec("INSERT INTO f2_tel_messages VALUES(?,?,?,?) ON CONFLICT(account_id,external_id) DO UPDATE SET body=excluded.body",account,external,unit,message.toString());
            touched.add(unit);
            var batch=row("SELECT min_sent FROM f2_tel_batches WHERE id=?",state[2]);
            if(batch[0]==null || instant.isBefore(Instant.parse(batch[0]))) exec("UPDATE f2_tel_batches SET min_sent=? WHERE id=?",instant.toString(),state[2]);
        }
        for(String unit:touched) {
            List<ObjectNode> members=members(unit); boolean complete=members.stream().allMatch(m->m.path("groupComplete").asBoolean());
            boolean eligible=complete && members.stream().flatMap(m->{List<JsonNode> a=new ArrayList<>();m.path("attachments").forEach(a::add);return a.stream();}).anyMatch(TelegramLibrary::audioVideo);
            exec("UPDATE f2_tel_units SET complete=?,eligible=? WHERE id=? AND payload IS NULL",complete?1:0,eligible?1:0,unit);
        }
        BigInteger cursor=high;
        for(var r:rows("SELECT m.external_id FROM f2_tel_messages m JOIN f2_tel_units u ON u.id=m.unit_id WHERE m.account_id=? AND u.complete=0",account)) cursor=cursor.min(number(r[0]).subtract(BigInteger.ONE));
        cursor=cursor.max(number(state[0]));
        exec("UPDATE f2_tel_accounts SET cursor=?,high_water=?,last_scan=?,revision=revision+1 WHERE id=?",cursor.toString(),high.toString(),now(),account);
        if(!finished) {
            ObjectNode next=obj().put("schemaVersion",1).put("accountId",account).put("afterMessageId",cursor.toString()).put("continuation",continuation);
            String nextTask=enqueue.add("SCAN_TEL_SAVED","TELEGRAM",account,"scan:"+state[2]+":"+continuation,next);
            exec("UPDATE f2_tel_accounts SET active_task=? WHERE id=?",nextTask,account);
        } else {
            boolean incomplete=row("SELECT id FROM f2_tel_units WHERE account_id=? AND complete=0 LIMIT 1",account)!=null;
            exec("UPDATE f2_tel_accounts SET active_task=NULL,initial_done=CASE WHEN ?=0 THEN 1 ELSE initial_done END WHERE id=?",incomplete?1:0,account);
            if(incomplete) issues.record(taskId,account,"INCOMPLETE_MEDIA_GROUP","WARNING");
            else issues.resolveObject(account,"INCOMPLETE_MEDIA_GROUP");
            for(var r:rows("SELECT id,batch_id FROM f2_tel_units WHERE account_id=? AND eligible=1 AND payload IS NULL",account)) {
                ObjectNode payload=freeze(account,r[0],directory(r[1]));
                String downloadTask=enqueue.add("DOWNLOAD_MEDIA","TELEGRAM",account,r[0],payload);
                exec("UPDATE f2_tel_units SET payload=?,task_id=?,status='QUEUED' WHERE id=?",payload.toString(),downloadTask,r[0]);
            }
        }
        exec("INSERT INTO f2_tel_pages VALUES(?,?)",eventId,page.toString());
    }
    public void scanStopped(String taskId) throws SQLException { writing(); exec("UPDATE f2_tel_accounts SET active_task=NULL,last_scan=?,revision=revision+1 WHERE active_task=?",now(),taskId); }
    public ArrayNode messages(String accountId) throws SQLException {
        ArrayNode out=array(); for(var r:rows("SELECT id FROM f2_tel_units WHERE (? IS NULL OR account_id=?) AND eligible=1 ORDER BY id",accountId,accountId)) out.add(message(r[0])); return out;
    }
    public ObjectNode message(String id) throws SQLException {
        var r=row("SELECT account_id,eligible,payload,status,task_id FROM f2_tel_units WHERE id=?",id); require(r!=null && "1".equals(r[1]),"NOT_FOUND");
        List<ObjectNode> members=members(id); ObjectNode first=members.getFirst(); ObjectNode out=obj().put("id",id).put("accountId",r[0]).put("externalId",first.path("externalId").asText()).put("sentAt",first.path("sentAt").asText()).put("groupId",nullable(first,"groupId")).put("eligible",true).put("taskId",r[4]);
        ArrayNode ids=array(),attachments=array(),links=array(); StringJoiner text=new StringJoiner("\n\n");
        for(var m:members) { ids.add(m.path("externalId").asText()); if(!m.path("text").asText().isBlank()) text.add(m.path("text").asText()); m.path("sourceLinks").forEach(links::add); }
        if(r[2]!=null) {
            ObjectNode frozen=read(r[2]);attachments=frozen.withArray("attachments").deepCopy();
            for(var a:attachments)for(var output:frozen.path("outputs"))if(output.path("attachmentId").equals(a.path("id")))
                ((ObjectNode)a).put(output.path("kind").asText().equals("METADATA")?"metadataRelativePath":"relativePath",output.path("relativePath").asText());
        } else for(var m:members) for(var a:m.path("attachments")) attachments.add(publicAttachment(r[0],m,a));
        int completed=0;
        for(var a:attachments) {
            String aid=a.path("id").asText();boolean verified=verifiedFile(id,r[4],aid)&&verifiedFile(id,r[4],aid+"-metadata");
            if(verified)completed++;((ObjectNode)a).put("downloadState",verified?"VERIFIED":r[3].equals("NEEDS_ACTION")?"NEEDS_ACTION":"QUEUED");
        }
        out.put("text",text.toString()); out.set("messageIds",ids); out.set("attachments",attachments); out.set("sourceLinks",links);
        out.set("downloadSummary",obj().put("state",r[3].equals("VERIFIED")?"COMPLETE":r[3].equals("NEEDS_ACTION")?"NEEDS_ACTION":"QUEUED").put("completed",completed).put("total",attachments.size())); return out;
    }
    public List<String> incompleteTasks(String accountId,Set<String> resourceIds) throws SQLException {
        List<String> out=new ArrayList<>(); for(var r:rows("SELECT id,task_id FROM f2_tel_units WHERE account_id=? AND eligible=1 AND status<>'VERIFIED' AND task_id IS NOT NULL",accountId)) if(resourceIds==null || resourceIds.contains(r[0])) {
            var tasks=rows("SELECT task_id FROM f3_tel_task_units WHERE unit_id=? ORDER BY task_id",r[0]);if(tasks.isEmpty())out.add(r[1]);else for(var t:tasks)out.add(t[0]);
        } return out;
    }
    public void replaceTask(String oldTask,String task) throws SQLException {writing();exec("UPDATE f2_tel_units SET task_id=?,status='QUEUED' WHERE task_id=?",task,oldTask);exec("INSERT OR IGNORE INTO f2_tel_files SELECT ?,output_id,body FROM f2_tel_files WHERE task_id=?",task,oldTask);exec("UPDATE f3_tel_task_units SET task_id=? WHERE task_id=?",task,oldTask);}
    public String batchId(String resourceId) throws SQLException {var r=row("SELECT batch_id FROM f2_tel_units WHERE id=?",resourceId);require(r!=null,"NOT_FOUND");return r[0];}
    public ObjectNode payloadForTask(String taskId) throws SQLException {var r=row("SELECT payload FROM f2_tel_units WHERE task_id=? OR id=(SELECT unit_id FROM f3_tel_task_units WHERE task_id=?)",taskId,taskId);require(r!=null,"NOT_FOUND");return read(r[0]);}
    public void bindTasks(String resourceId,JsonNode assignments) throws SQLException {writing();for(JsonNode assignment:assignments)exec("INSERT INTO f3_tel_task_units VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET unit_id=excluded.unit_id,outputs=excluded.outputs",text(assignment,"taskId"),resourceId,assignment.path("outputs").toString());}
    private boolean verifiedFile(String unit,String primary,String output) throws SQLException {return row("SELECT output_id FROM f2_tel_files WHERE output_id=? AND (task_id=? OR task_id IN (SELECT task_id FROM f3_tel_task_units WHERE unit_id=?)) LIMIT 1",output,primary,unit)!=null;}
    public void downloadFacts(String taskId,JsonNode files,boolean succeeded) throws SQLException {
        writing();var unit=row("SELECT payload,id,task_id FROM f2_tel_units WHERE task_id=? OR id=(SELECT unit_id FROM f3_tel_task_units WHERE task_id=?)",taskId,taskId);require(unit!=null,"NOT_FOUND");ObjectNode payload=read(unit[0]);Map<String,JsonNode> expected=new HashMap<>();
        var scoped=row("SELECT outputs FROM f3_tel_task_units WHERE task_id=?",taskId);JsonNode taskOutputs=scoped==null?payload.path("outputs"):readArray(scoped[0]);taskOutputs.forEach(o->expected.put(o.path("id").asText(),o));
        require(files.isArray(),"VERIFICATION_FAILED");Set<String> seen=new HashSet<>();
        for(var f:files) {
            String id=text(f,"outputId");JsonNode output=expected.get(id);require(output!=null&&seen.add(id)&&f.path("verified").asBoolean()&&output.path("relativePath").equals(f.path("relativePath")),"VERIFICATION_FAILED");
            require(text(f,"sizeBytes").matches("0|[1-9][0-9]*")&&text(f,"sha256").matches("[0-9a-fA-F]{64}"),"VERIFICATION_FAILED");
            if(output.hasNonNull("sizeBytes"))require(output.path("sizeBytes").equals(f.path("sizeBytes")),"VERIFICATION_FAILED");
            exec("INSERT INTO f2_tel_files VALUES(?,?,?) ON CONFLICT(task_id,output_id) DO UPDATE SET body=excluded.body",taskId,id,f.toString());
        }
        if(succeeded)require(seen.equals(expected.keySet()),"VERIFICATION_FAILED");
        boolean complete=true;for(JsonNode output:payload.path("outputs"))complete &= verifiedFile(unit[1],unit[2],text(output,"id"));
        exec("UPDATE f2_tel_units SET status=? WHERE id=?",complete?"VERIFIED":succeeded?"QUEUED":"NEEDS_ACTION",unit[1]);
    }
    private JsonNode readArray(String value) {try{return json.readTree(value);}catch(Exception e){throw new IllegalStateException("Invalid frozen output mapping",e);}}
    private ObjectNode freeze(String account,String unit,String directory) throws SQLException {
        List<ObjectNode> members=members(unit); ArrayNode attachments=array(),outputs=array(),ids=array(); Set<String> names=new HashSet<>();
        String base="Telegram/"+directory+"/"+members.getFirst().path("externalId").asText();
        for(var m:members) { ids.add(m.path("externalId").asText()); for(var a:m.path("attachments")) {
            ObjectNode item=publicAttachment(account,m,a); String name=segment(item.path("name").asText()); String candidate=name; int n=2;
            while(names.contains(candidate.toLowerCase(Locale.ROOT))||names.contains((candidate+".metadata.json").toLowerCase(Locale.ROOT)))candidate=name+" ("+(n++)+")";
            names.add(candidate.toLowerCase(Locale.ROOT));names.add((candidate+".metadata.json").toLowerCase(Locale.ROOT));
            String path=base+"/"+candidate; attachments.add(item);
            String kind=item.path("mediaType").asText(); if(kind.equals("UNKNOWN")) kind="DOCUMENT";
            ObjectNode output=obj().put("id",item.path("id").asText()).put("attachmentId",item.path("id").asText()).put("relativePath",path).put("kind",kind);output.set("sizeBytes",item.path("sizeBytes"));output.set("trackIds",array());outputs.add(output);
            ObjectNode metadataOutput=obj().put("id",item.path("id").asText()+"-metadata").put("attachmentId",item.path("id").asText()).put("relativePath",path+".metadata.json").put("kind","METADATA").putNull("sizeBytes");metadataOutput.set("trackIds",array());outputs.add(metadataOutput);
        } }
        ObjectNode payload=obj().put("schemaVersion",1).put("accountId",account).put("resourceId",unit).put("archivePolicy","AUTO"); payload.set("messageIds",ids);payload.set("attachments",attachments);payload.set("outputs",outputs);
        ObjectNode metadata=obj().put("schemaVersion",1); metadata.set("messages",json.valueToTree(members)); payload.set("metadata",metadata); return payload;
    }
    private ObjectNode publicAttachment(String account,JsonNode message,JsonNode a) {
        ObjectNode item=((ObjectNode)a).deepCopy(); item.put("id",stable("attachment-",account,message.path("externalId").asText(),text(a,"externalId"))).put("messageId",message.path("externalId").asText());
        if(audioVideo(a) && !Set.of("VIDEO","AUDIO").contains(a.path("mediaType").asText())) item.put("mediaType",a.path("mimeType").asText().toLowerCase(Locale.ROOT).startsWith("video/")?"VIDEO":"AUDIO");
        return item;
    }
    private List<ObjectNode> members(String unit) throws SQLException { List<ObjectNode> out=new ArrayList<>(); for(var r:rows("SELECT body FROM f2_tel_messages WHERE unit_id=?",unit)) out.add(read(r[0])); out.sort(Comparator.comparing(m->number(m.path("externalId").asText()))); return out; }
    private String directory(String batch) throws SQLException {
        var r=row("SELECT min_sent,directory FROM f2_tel_batches WHERE id=?",batch); if(r[1]!=null)return r[1]; require(r[0]!=null,"PROVIDER_CHANGED");
        String base=DateTimeFormatter.ofPattern("yyyy.MM.dd-HH.mm.ss").withZone(ZoneId.of("Asia/Shanghai")).format(Instant.parse(r[0])); String path=base;int n=2;
        while(row("SELECT id FROM f2_tel_batches WHERE directory=?",path)!=null) path=base+" ("+(n++)+")";
        exec("UPDATE f2_tel_batches SET directory=? WHERE id=?",path,batch); return path;
    }
    private static boolean audioVideo(JsonNode a) { String type=a.path("mediaType").asText(),mime=a.path("mimeType").asText("").toLowerCase(Locale.ROOT); return type.equals("VIDEO")||type.equals("AUDIO")||mime.startsWith("video/")||mime.startsWith("audio/"); }
    private static BigInteger number(String s) { require(s.matches("0|[1-9][0-9]*"),"PROVIDER_CHANGED"); return new BigInteger(s); }
    private static void validateAttachments(JsonNode attachments) {
        require(attachments.isArray(),"PROVIDER_CHANGED"); Set<String> seen=new HashSet<>(); for(JsonNode a:attachments) {
            require(seen.add(text(a,"externalId")),"PROVIDER_CHANGED"); text(a,"name"); require(Set.of("VIDEO","AUDIO","PHOTO","DOCUMENT","UNKNOWN").contains(text(a,"mediaType")),"PROVIDER_CHANGED");
            if(a.hasNonNull("sizeBytes")) number(text(a,"sizeBytes"));
        }
    }
}
