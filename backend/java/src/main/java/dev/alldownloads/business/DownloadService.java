package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.math.BigInteger;
import java.nio.file.Path;
import java.sql.SQLException;
import java.time.Instant;
import java.util.*;
import static dev.alldownloads.business.Json.*;

public final class DownloadService {
    private final BusinessStore store;
    private final CatalogService catalog;
    private final OperationQueue queue;
    private final PathPlanner paths;
    private DownloadManagement management;
    private StorageSettings storageSettings;
    public void management(DownloadManagement value,StorageSettings settings) {management=value;storageSettings=settings;}
    private dev.alldownloads.feedback2.QuarkCatalog quark;
    public void useQuark(dev.alldownloads.feedback2.QuarkCatalog value) {quark=value;}
    public DownloadService(BusinessStore store,CatalogService catalog,OperationQueue queue) {this.store=store;this.catalog=catalog;this.queue=queue;paths=new PathPlanner(store);}
    public void configureDefault(String path) throws SQLException {
        ObjectNode old=store.optional("target","default");
        if(old!=null&&store.optional("settings","global")!=null)return;
        if(old!=null&&text(old,"path").equals(path))return;
        String revision=old==null?"1":Long.toString(Long.parseLong(text(old,"revision"))+1);
        ObjectNode target=object("id","default","revision",revision,"status",path.isBlank()?"INVALID":"VALIDATING","path",path,"label",path.isBlank()?"尚未配置下载目录":path,"rootId",null,"archivePolicy","AUTO","error",path.isBlank()?object("code","INVALID_STORAGE_ROOT","message","尚未配置下载目录"):null);
        if(!path.isBlank())validatePath(path);
        store.put("target","default",target);
        if(!path.isBlank())queue.enqueue("VALIDATE_TARGET","BILIBILI","target:default:"+revision,null,revision,object("schemaVersion",1,"targetId","default","path",path));
    }
    public ObjectNode settings() throws SQLException {return storageSettings.settings();}
    public ObjectNode validateTarget(JsonNode request) throws SQLException {
        String path=required(request,"path");validatePath(path);String targetId=id();
        ObjectNode target=object("id",targetId,"revision","1","status","VALIDATING","path",path,"label",path,"rootId",null,"archivePolicy","NONE","error",null);store.put("target",targetId,target);
        String task=queue.enqueue("VALIDATE_TARGET","BILIBILI","target:"+targetId,null,"1",object("schemaVersion",1,"targetId",targetId,"path",path));return object("targetId",targetId,"taskId",task);
    }
    static void validatePath(String path) {if(path.length()>220||path.startsWith("\\\\")||path.startsWith("//")||path.indexOf('\0')>=0||!Path.of(path).isAbsolute())throw new Failure(422,"INVALID_STORAGE_ROOT","必须是运行主机的本机绝对目录");}
    public void targetResult(JsonNode operation,JsonNode result) throws SQLException {
        String targetId=required(operation.path("payload"),"targetId");if(!targetId.equals(required(result,"targetId"))||!result.path("writable").asBoolean(false))throw Failure.conflict("VERIFICATION_FAILED");
        ObjectNode target=store.get("target",targetId);String canonical=required(result,"canonicalPath");validatePath(canonical);
        if(!Path.of(canonical).normalize().equals(Path.of(text(target,"path")).toAbsolutePath().normalize()))throw Failure.conflict("TARGET_CHANGED");
        if(!text(operation,"storageRevision").equals(text(target,"revision")))throw Failure.conflict("STORAGE_CHANGED");
        target.put("path",canonical);target.put("label",canonical);target.put("rootId",required(result,"rootId"));target.put("status","VALID");target.putNull("error");store.put("target",targetId,target);
    }
    private ObjectNode selectedTarget(JsonNode request) throws SQLException {
        JsonNode ref=request.path("target");ObjectNode target=store.get("target",ref.isObject()?required(ref,"id"):"default");
        if(ref.isObject()&&!text(target,"revision").equals(required(ref,"revision")))throw Failure.conflict("STORAGE_CHANGED");
        if(!text(target,"status").equals("VALID"))throw new Failure(422,"INVALID_STORAGE_ROOT","下载目录尚未通过执行程序验证");return target;
    }
    public ObjectNode preview(JsonNode request) throws SQLException {
        if(required(request,"platform").equals("QUARK"))return quarkPreview(request);
        if(!required(request,"platform").equals("BILIBILI"))throw Failure.bad("INVALID_INPUT","当前提供方仅支持 Bilibili 预览");
        ObjectNode target=selectedTarget(request);JsonNode selection=request.path("selection");String resource=required(selection,"resource"),mode=required(selection,"mode");
        if(!Set.of("BILIBILI_CONTENTS","BILIBILI_PARTS").contains(resource))throw Failure.bad("INVALID_INPUT","Unsupported selection resource");
        List<ObjectNode> contents=new ArrayList<>();Set<String> selectedParts=new HashSet<>();
        if(mode.equals("QUERY")) {
            Map<String,String> filters=new HashMap<>();selection.path("filters").fields().forEachRemaining(e->{if(!Set.of("sourceId","q","type","state","sort","from","to").contains(e.getKey())||!e.getValue().isTextual())throw Failure.bad("INVALID_INPUT","Unsupported selection filter");filters.put(e.getKey(),e.getValue().asText());});
            Set<String> exclude=selection.has("excludeIds")?new HashSet<>(strings(selection.path("excludeIds"))):Set.of();
            for(JsonNode content:catalog.filtered(filters)) {
                if(resource.equals("BILIBILI_CONTENTS")) {if(!exclude.contains(text(content,"id")))contents.add((ObjectNode)content);}
                else {boolean any=false;for(JsonNode unit:content.path("mediaUnits"))if(!exclude.contains(text(unit,"id"))) {selectedParts.add(text(unit,"id"));any=true;}if(any)contents.add((ObjectNode)content);}
            }
        } else if(mode.equals("EXPLICIT")) {
            Set<String> ids=new LinkedHashSet<>(strings(selection.path("ids")));if(ids.isEmpty())throw Failure.bad("EMPTY_SELECTION","请选择要下载的内容");
            if(resource.equals("BILIBILI_CONTENTS"))for(String id:ids)contents.add(catalog.content(id));
            else {selectedParts.addAll(ids);Set<String> found=new HashSet<>();for(JsonNode value:store.all("content")) {boolean any=false;for(JsonNode unit:value.path("mediaUnits"))if(ids.contains(text(unit,"id"))) {found.add(text(unit,"id"));any=true;}if(any)contents.add(catalog.content(text(value,"id")));}if(!found.equals(ids))throw Failure.missing();}
        } else throw Failure.bad("INVALID_INPUT","Unsupported selection mode");
        if(contents.isEmpty())throw Failure.bad("EMPTY_SELECTION","没有符合条件的内容");
        Map<String,JsonNode> custom=new HashMap<>();
        if(selection.has("mediaSelections")) {
            if(!selection.path("mediaSelections").isArray())throw Failure.bad("INVALID_INPUT","mediaSelections must be array");
            for(JsonNode choice:selection.path("mediaSelections")) {String id=required(choice,"contentId");if(custom.put(id,choice)!=null)throw Failure.bad("INVALID_INPUT","Duplicate content selection");if(strings(choice.path("trackIds")).isEmpty())throw Failure.bad("EMPTY_SELECTION","已取消全部媒体项，请重新选择");}
        }
        Set<String> selectedContentIds=new HashSet<>();contents.forEach(c->selectedContentIds.add(text(c,"id")));if(!selectedContentIds.containsAll(custom.keySet()))throw Failure.bad("INVALID_INPUT","媒体选择不属于当前清单");
        List<JsonNode> items=new ArrayList<>();List<JsonNode> plans=new ArrayList<>();
        for(ObjectNode content:contents) {
            JsonNode choice=custom.get(text(content,"id"));Set<String> chosen=choice==null?null:new HashSet<>(strings(choice.path("trackIds")));
            if(choice!=null&&!text(content,"revision").equals(required(choice,"contentRevision")))throw Failure.conflict("VERSION_CHANGED");
            Set<String> validIds=new HashSet<>();int unitsAdded=0;
            for(JsonNode value:content.path("mediaUnits")) {
                if(resource.equals("BILIBILI_PARTS")&&!selectedParts.contains(text(value,"id")))continue;
                List<JsonNode> tracks=new ArrayList<>();for(JsonNode track:value.path("tracks")) {
                    if(track.path("available").asBoolean(false))validIds.add(text(track,"id"));
                    if(track.path("available").asBoolean(false)&&(chosen==null||chosen.contains(text(track,"id"))))tracks.add(track);
                }
                if(tracks.isEmpty()&&chosen!=null)continue;
                ObjectNode unit=(ObjectNode)value.deepCopy();unit.set("tracks",array(tracks));
                String base=paths.unit(content,value);List<JsonNode> outputs=outputs(content,unit,base);String itemId=PathPlanner.digest(text(content,"id")+":"+text(unit,"id")+":"+text(content,"revision")+":"+canonical(array(outputs)));
                ObjectNode item=object("id",itemId,"contentId",text(content,"id"),"partId",text(unit,"id"),"title",text(unit,"title").isBlank()?text(content,"title"):text(content,"title")+" / "+text(unit,"title"),"version",text(content,"revision"),"relativePath",base,"sizeBytes",totalSize(outputs),"decision","DOWNLOAD","reasonCode","PENDING_DOWNLOAD","reason","尚未下载所选内容","actionLabel","下载所选内容","selectedTrackIds",tracks.stream().map(t->text(t,"id")).sorted().toList(),"mergePolicy",outputs.stream().anyMatch(o->text(o,"kind").equals("MUXED"))?"MUX":"SEPARATE","outputs",outputs,"downloadState","NOT_REQUESTED","archiveState","NOT_REQUESTED");
                classify(item,content,value,target);items.add(item);unitsAdded++;
                ObjectNode payload=object("schemaVersion",1,"contentId",content.path("id"),"contentRevision",content.path("revision"),"contentKind",content.path("kind"),"externalId",content.path("externalId"),"externalIdType",content.path("externalIdType"),"qualityPolicy","HIGHEST_AVAILABLE","archivePolicy",target.path("archivePolicy"),"units",List.of(unit),"outputs",outputs);
                plans.add(object("itemId",itemId,"key",planKey(item,target),"destination",object("rootId",target.path("rootId"),"rootPath",target.path("path"),"relativePath",paths.content(content)),"payload",payload));
            }
            if(chosen!=null&&!validIds.containsAll(chosen))throw Failure.bad("INVALID_INPUT","选项不可下载或不属于所选分段");
            if(unitsAdded==0&&chosen==null) {
                String reason=reason(content);items.add(object("id",text(content,"id"),"contentId",text(content,"id"),"partId",null,"title",content.path("title"),"version",content.path("revision"),"relativePath",paths.content(content),"sizeBytes",null,"decision","BLOCKED","reasonCode",reason,"reason",reasonLabel(reason),"actionLabel",reasonLabel(reason)+"，不能下载","selectedTrackIds",List.of(),"mergePolicy","SEPARATE","outputs",List.of(),"downloadState","NOT_REQUESTED","archiveState","NOT_REQUESTED"));
            }
        }
        if(items.isEmpty())throw Failure.bad("EMPTY_SELECTION","请选择要下载的媒体项");
        String previewId=id();ObjectNode preview=object("id",previewId,"status","READY","platform","BILIBILI","expiresAt",store.clock.instant().plusSeconds(600).toString(),"storageRevision",store.get("target","default").path("revision"),"target",target,"archivePolicy",target.path("archivePolicy"),"items",items,"summary",object("download",count(items,"DOWNLOAD"),"reuse",count(items,"REUSE"),"blocked",count(items,"BLOCKED"),"total",items.size()),"warnings",List.of());
        store.put("preview",previewId,preview);store.put("previewPlans",previewId,object("plans",plans));return object("previewId",previewId,"taskId",null);
    }
    private static long count(List<JsonNode> values,String decision) {return values.stream().filter(v->text(v,"decision").equals(decision)).count();}
    private List<JsonNode> outputs(JsonNode content,JsonNode unit,String base) throws SQLException {
        List<JsonNode> tracks=list(unit.path("tracks")),out=new ArrayList<>();List<JsonNode> video=tracks.stream().filter(t->text(t,"kind").equals("VIDEO")).toList(),audio=tracks.stream().filter(t->text(t,"kind").equals("AUDIO")).toList();
        if(!video.isEmpty()&&!audio.isEmpty()) {List<JsonNode> av=new ArrayList<>(video);av.addAll(audio);out.add(output(content,base,"MUXED",av,"media.mp4"));}
        else {for(JsonNode track:video)out.add(output(content,base,"VIDEO",List.of(track),"video.mp4"));for(JsonNode track:audio)out.add(output(content,base,"AUDIO",List.of(track),"audio.m4a"));}
        for(JsonNode track:tracks) {String kind=text(track,"kind");if(kind.equals("SUBTITLE"))out.add(output(content,base,kind,List.of(track),"subtitle-"+PathPlanner.safe(text(track,"language"))+"-"+PathPlanner.digest(text(track,"id")).substring(0,8)+".srt"));else if(kind.equals("DANMAKU"))out.add(output(content,base,kind,List.of(track),"danmaku.xml"));}
        return out;
    }
    private JsonNode output(JsonNode content,String base,String kind,List<JsonNode> tracks,String filename) throws SQLException {
        List<String> ids=tracks.stream().map(t->text(t,"id")).sorted().toList();String key=PathPlanner.digest(text(content,"id")+":"+text(content,"revision")+":"+kind+":"+String.join(",",ids));
        // Remuxing and subtitle conversion change byte counts. Track sizes remain visible in details,
        // but no input size is misrepresented as the final output size.
        String path=paths.output(key,base+"/"+filename);return object("id",key,"relativePath",path,"trackIds",ids,"kind",kind,"sizeBytes",null);
    }
    private static String totalSize(List<JsonNode> values) {BigInteger total=BigInteger.ZERO;if(values.isEmpty())return null;for(JsonNode value:values) {if(!value.path("sizeBytes").isTextual())return null;total=total.add(new BigInteger(text(value,"sizeBytes")));}return total.toString();}
    private static String planKey(JsonNode item,JsonNode target) {return "download:"+PathPlanner.digest(text(item,"id")+":"+text(target,"rootId")+":"+text(target,"path")+":"+text(target,"archivePolicy"));}
    private void classify(ObjectNode item,JsonNode content,JsonNode unit,JsonNode target) throws SQLException {
        if(!text(content,"availability").equals("AVAILABLE")||!text(unit,"availability").equals("AVAILABLE")||item.path("outputs").isEmpty()) {
            String reason=!text(content,"availability").equals("AVAILABLE")?reason(content):reason(unit);item.put("decision","BLOCKED");item.put("reasonCode",reason);item.put("reason",reasonLabel(reason));item.put("actionLabel",reasonLabel(reason)+"，不能下载");return;
        }
        int existing=0,archived=0;for(JsonNode output:item.path("outputs")) {ObjectNode artifact=store.optional("file",fileKey(target,output));if(artifact!=null&&text(artifact,"localState").equals("VERIFIED"))existing++;if(artifact!=null&&text(artifact,"archiveState").equals("VERIFIED"))archived++;}
        boolean custom=text(target,"archivePolicy").equals("NONE");int total=item.path("outputs").size();
        if(existing==total||!custom&&archived==total) {item.put("decision","REUSE");item.put("reasonCode",!custom&&archived==total?"ARCHIVE_VERIFIED":"LOCAL_VERIFIED");item.put("downloadState",existing==total?"VERIFIED":"MISSING");item.put("archiveState",archived==total?"VERIFIED":"NOT_REQUESTED");item.put("reason",!custom&&archived==total?"云端已核验归档":"所选文件已在当前目录下载完成");item.put("actionLabel",!custom&&archived==total?"已归档，无需下载":"已下载，无需重复下载");return;}
        String[] active=store.row("SELECT task_id FROM operations WHERE resource_key=? AND status NOT IN ('SUCCEEDED','CANCELLED','FAILED')",planKey(item,target));
        if(active!=null) {item.put("decision","REUSE");item.put("reasonCode","DOWNLOADING");item.put("reason","所选内容正在处理");item.put("actionLabel","正在下载，复用当前任务");item.put("downloadState","DOWNLOADING");return;}
        if(existing>0) {item.put("reasonCode","PARTIAL_EXISTS");item.put("reason","仅部分所选文件已存在");item.put("downloadState","PARTIAL");item.put("actionLabel",custom?"补齐到指定目录，不归档":"补齐所选内容");}
        else if(custom) {item.put("reasonCode","CUSTOM_TARGET_NO_ARCHIVE");item.put("reason","仅本批次使用指定目录");item.put("actionLabel","下载到指定目录，不归档");}
    }
    private String reason(JsonNode value) {if(text(value,"availability").equals("REMOVED")&&text(value,"availabilityReason").equals("RESOURCE_DELETED"))return "RESOURCE_DELETED";String reason=text(value,"availabilityReason");return Set.of("PERMISSION_DENIED","REGION_RESTRICTED","PAYMENT_REQUIRED","NETWORK_TEMPORARY").contains(reason)?reason:"STATE_UNKNOWN";}
    private static String reasonLabel(String reason) {return reason.equals("STATE_UNKNOWN")?"状态待确认":OperationQueue.message(reason);}
    private static String fileKey(JsonNode target,JsonNode output) {return text(target,"rootId")+":"+text(output,"id");}
    public ObjectNode getPreview(String id) throws SQLException {ObjectNode preview=store.get("preview",id);if(!Instant.parse(text(preview,"expiresAt")).isAfter(store.clock.instant()))preview.put("status","EXPIRED");return preview;}
    public ObjectNode submit(JsonNode request) throws SQLException {
        if(request.size()!=1)throw Failure.bad("INVALID_INPUT","Submit only the previewId; refresh changes first");String previewId=required(request,"previewId");ObjectNode preview=getPreview(previewId);
        String[] prior=store.row("SELECT body FROM objects WHERE kind='order' AND json_extract(body,'$.previewId')=?",previewId);if(prior!=null) {JsonNode order=parse(prior[0]);return object("orderId",order.path("id"),"projectIds",order.path("projectIds"),"taskIds",order.path("taskIds"),"reused",true);}
        if(!text(preview,"status").equals("READY"))throw Failure.conflict("PREVIEW_EXPIRED");JsonNode target=preview.path("target");ObjectNode current=store.get("target",text(target,"id"));
        if(!current.equals(target)||text(target,"id").equals("default")&&!text(preview,"storageRevision").equals(text(store.get("target","default"),"revision")))throw Failure.conflict("STORAGE_CHANGED");
        List<JsonNode> items=list(preview.path("items"));
        if(text(preview,"platform").equals("QUARK"))quark.validateFrozen(store.get("previewNodes",previewId).path("nodes"));
        else for(JsonNode item:items)if(!text(store.get("content",text(item,"contentId")),"revision").equals(text(item,"version")))throw Failure.conflict("VERSION_CHANGED");
        if(items.stream().allMatch(i->text(i,"decision").equals("BLOCKED")))throw new Failure(422,"EMPTY_SELECTION","所有选项都暂不可下载");
        Map<String,JsonNode> plans=new HashMap<>();for(JsonNode plan:store.get("previewPlans",previewId).path("plans"))plans.put(text(plan,"itemId"),plan);
        Set<String> tasks=new LinkedHashSet<>();
        for(JsonNode item:items) {
            if(text(item,"decision").equals("BLOCKED"))continue;JsonNode plan=plans.get(text(item,"id"));if(plan==null)continue;
            if(text(item,"decision").equals("REUSE")) {if(text(item,"reasonCode").equals("DOWNLOADING")) {String[] active=store.row("SELECT task_id FROM operations WHERE resource_key=? ORDER BY created DESC LIMIT 1",text(plan,"key"));if(active!=null)tasks.add(active[0]);}continue;}
            ObjectNode payload=(ObjectNode)plan.path("payload").deepCopy();
            // A previous order may have finished after the preview. Only remove exactly proven outputs.
            List<JsonNode> missing=new ArrayList<>();
            for(JsonNode output:payload.path("outputs")) {
                ObjectNode file=store.optional("file",fileKey(target,output));if(file!=null&&text(file,"localState").equals("VERIFIED"))continue;
                String[] claim=store.row("SELECT task_id FROM output_claims WHERE root_id=? AND output_id=?",text(target,"rootId"),text(output,"id"));
                if(claim!=null) {String status=text(store.get("task",claim[0]),"status");if(!Set.of("FAILED","CANCELLED","SUCCEEDED").contains(status)) {tasks.add(claim[0]);continue;}}
                missing.add(output);
            }
            if(missing.isEmpty())continue;
            if(text(preview,"platform").equals("BILIBILI")) {
                Set<String> neededTracks=new HashSet<>();missing.forEach(o->neededTracks.addAll(strings(o.path("trackIds"))));List<JsonNode> units=new ArrayList<>();for(JsonNode u:payload.path("units")) {ObjectNode unit=(ObjectNode)u.deepCopy();unit.set("tracks",array(list(u.path("tracks")).stream().filter(t->neededTracks.contains(text(t,"id"))).toList()));if(!unit.path("tracks").isEmpty())units.add(unit);}payload.set("units",array(units));
            } else {Set<String> ids=new HashSet<>();missing.forEach(o->ids.add(text(o,"nodeId")));payload.set("nodes",array(list(payload.path("nodes")).stream().filter(n->ids.contains(text(n,"id"))).toList()));}
            payload.set("outputs",array(missing));
            String task=queue.enqueue("DOWNLOAD_MEDIA",text(preview,"platform"),text(plan,"key"),plan.path("destination"),text(preview,"storageRevision"),payload);if(plan.hasNonNull("accountRef"))queue.account(task,text(plan,"accountRef"));store.put("taskTarget",task,target);tasks.add(task);
            for(JsonNode output:missing)store.update("INSERT INTO output_claims VALUES(?,?,?) ON CONFLICT(root_id,output_id) DO UPDATE SET task_id=excluded.task_id",text(target,"rootId"),text(output,"id"),task);
        }
        String orderId=id();ObjectNode order=object("id",orderId,"previewId",previewId,"platform",preview.path("platform"),"status",tasks.isEmpty()?"SUCCEEDED":"QUEUED","target",target,"archivePolicy",preview.path("archivePolicy"),"items",items,"taskIds",tasks,"createdAt",store.now());store.put("order",orderId,order);List<String> projects=management.registerOrder(order);return object("orderId",orderId,"projectIds",projects,"taskIds",tasks,"reused",tasks.isEmpty());
    }
    public void downloadResult(JsonNode operation,JsonNode result) throws SQLException {
        recordDownloadFiles(operation,result,true);
    }
    public void recordDownloadFiles(JsonNode operation,JsonNode result,boolean complete) throws SQLException {
        JsonNode payload=operation.path("payload");Map<String,JsonNode> expected=new HashMap<>();for(JsonNode output:payload.path("outputs"))expected.put(text(output,"id"),output);
        if(!result.path("files").isArray()||(complete&&result.path("files").size()!=expected.size()))throw Failure.conflict("VERIFICATION_FAILED");
        Set<String> seen=new HashSet<>();List<JsonNode> files=new ArrayList<>();
        JsonNode target=store.get("taskTarget",text(operation,"taskId"));
        for(JsonNode file:result.path("files")) {
            String outputId=required(file,"outputId");JsonNode output=expected.get(outputId);if(output==null||!seen.add(outputId)||!text(output,"relativePath").equals(required(file,"relativePath"))||!file.path("verified").asBoolean(false)||!required(file,"sizeBytes").matches("[0-9]+")||!required(file,"sha256").matches("[a-f0-9]{64}"))throw Failure.conflict("VERIFICATION_FAILED");
            Set<String> media=new HashSet<>(strings(file.path("mediaKinds")));Set<String> required=text(output,"kind").equals("MUXED")?Set.of("VIDEO","AUDIO"):Set.of(text(output,"kind"));if(!media.containsAll(required))throw Failure.conflict("VERIFICATION_FAILED");
            if(output.path("sizeBytes").isTextual()&&!text(output,"sizeBytes").equals(text(file,"sizeBytes")))throw Failure.conflict("VERIFICATION_FAILED");
            ObjectNode record=object("id",outputId,"contentId",payload.path("contentId"),"contentRevision",payload.path("contentRevision"),"trackIds",output.path("trackIds"),"target",target,"output",output,"evidence",file,"localState","VERIFIED","archiveState","NOT_REQUESTED","archivePolicy",payload.path("archivePolicy"),"operationId",operation.path("operationId"));files.add(record);
        }
        for(JsonNode file:files)store.put("file",text(operation.path("destination"),"rootId")+":"+text(file,"id"),file);
        if(text(operation,"platform").equals("BILIBILI"))updateContentSummary(text(payload,"contentId"));
        else if(text(operation,"platform").equals("QUARK")&&complete)quark.downloadVerified(payload.path("nodes"));
        for(JsonNode warning:result.path("warnings"))if(warning.isTextual())queue.issue(text(operation,"taskId"),text(payload,"contentId"),"SUPPLEMENT_WARNING","WARNING");
    }
    private void updateContentSummary(String contentId) throws SQLException {
        ObjectNode content=store.get("content",contentId);Set<String> expected=new HashSet<>(),actual=new HashSet<>();for(JsonNode unit:content.path("mediaUnits"))for(JsonNode track:unit.path("tracks"))if(track.path("available").asBoolean())expected.add(text(track,"id"));
        for(JsonNode file:store.all("file"))if(text(file,"contentId").equals(contentId)&&text(file,"contentRevision").equals(text(content,"revision"))&&text(file,"localState").equals("VERIFIED"))actual.addAll(strings(file.path("trackIds")));
        content.set("downloadSummary",object("state",!expected.isEmpty()&&actual.containsAll(expected)?"VERIFIED":actual.isEmpty()?"NOT_REQUESTED":"PARTIAL","completedTracks",actual.size(),"totalTracks",expected.size()));store.put("content",contentId,content);
    }
    public ObjectNode defaultTarget() throws SQLException {return selectedTarget(object());}
    public String savedDownload(String key,String account,String batch,ObjectNode payload) throws SQLException {
        ObjectNode target=defaultTarget();String relative=required(payload.path("outputs").get(0),"relativePath");relative=relative.substring(0,relative.lastIndexOf('/'));
        List<JsonNode> assignments=new ArrayList<>();
        for(JsonNode attachment:payload.path("attachments")) {
            ObjectNode scoped=payload.deepCopy();List<JsonNode> outputs=list(payload.path("outputs")).stream().filter(o->text(o,"attachmentId").equals(text(attachment,"id"))).toList();
            scoped.set("attachments",array(List.of(attachment)));scoped.set("outputs",array(outputs));
            String task=queue.enqueue("DOWNLOAD_MEDIA","TELEGRAM",key+":"+text(attachment,"id"),object("rootId",target.path("rootId"),"rootPath",target.path("path"),"relativePath",relative),text(target,"revision"),scoped);
            queue.account(task,account);store.put("taskTarget",task,target);assignments.add(object("taskId",task,"outputs",outputs));
            for(JsonNode output:outputs)store.update("INSERT INTO output_claims VALUES(?,?,?) ON CONFLICT(root_id,output_id) DO UPDATE SET task_id=excluded.task_id",text(target,"rootId"),text(output,"id"),task);
        }
        if(assignments.isEmpty())throw Failure.bad("EMPTY_SELECTION","收藏消息没有可执行附件");
        String primary=text(assignments.getFirst(),"taskId"),resource=required(payload,"resourceId");
        store.put("savedAssignments",primary,object("assignments",assignments));store.put("savedPolicy",resource,object("target",target,"archiveTarget",storageSettings.frozenArchiveTarget()));
        registerSavedInstance(batch,payload);return primary;
    }
    public JsonNode savedAssignments(String primary) throws SQLException {return store.get("savedAssignments",primary).path("assignments");}
    public void registerSavedInstance(String batch,JsonNode payload) throws SQLException {
        String resource=required(payload,"resourceId");JsonNode policy=store.get("savedPolicy",resource),target=policy.path("target");List<JsonNode> items=new ArrayList<>();
        String title="收藏消息 "+payload.path("messageIds").get(0).asText();
        for(JsonNode attachment:payload.path("attachments")) {
            List<JsonNode> outputs=list(payload.path("outputs")).stream().filter(o->text(o,"attachmentId").equals(text(attachment,"id"))).toList();
            items.add(object("id",attachment.path("id"),"contentId",resource,"attachmentId",attachment.path("id"),"title",attachment.path("name"),"projectTitle",title,"outputs",outputs,"decision","DOWNLOAD"));
        }
        List<String> projectIds=management.registerBatch(batch,"TELEGRAM",target,items,store.now(),policy.path("archiveTarget"));
        ObjectNode order=store.optional("order",batch);if(order==null)order=object("id",batch,"previewId",null,"platform","TELEGRAM","status","QUEUED","target",target,"archivePolicy","AUTO","items",List.of(),"taskIds",List.of(),"projectIds",List.of(),"createdAt",store.now());
        List<JsonNode> allItems=new ArrayList<>(list(order.path("items")));allItems.addAll(items);order.set("items",array(allItems));
        Set<String> allProjects=new LinkedHashSet<>(strings(order.path("projectIds")));allProjects.addAll(projectIds);order.set("projectIds",MAPPER.valueToTree(allProjects));
        Set<String> tasks=new LinkedHashSet<>(strings(order.path("taskIds")));for(String project:projectIds)for(JsonNode child:management.project(project).path("items"))tasks.addAll(strings(child.path("taskIds")));order.set("taskIds",MAPPER.valueToTree(tasks));store.put("order",batch,order);
    }
    private ObjectNode quarkPreview(JsonNode request) throws SQLException {
        JsonNode selection=request.path("selection");if(!required(selection,"resource").equals("QUARK_NODES")||selection.has("mediaSelections"))throw Failure.bad("INVALID_INPUT","夸克清单只接受网盘节点选择");
        String mode=required(selection,"mode"),share=null;Set<String> ids=null,exclude=selection.has("excludeIds")?new HashSet<>(strings(selection.path("excludeIds"))):Set.of();
        if(mode.equals("EXPLICIT"))ids=new LinkedHashSet<>(strings(selection.path("ids")));else if(mode.equals("QUERY")){share=required(selection.path("filters"),"shareId");if(selection.path("filters").size()!=1)throw Failure.bad("INVALID_INPUT","Unsupported Quark filter");}else throw Failure.bad("INVALID_INPUT","Unsupported selection mode");
        ObjectNode target=selectedTarget(request);var nodes=quark.expand(ids,share,exclude);List<JsonNode> items=new ArrayList<>(),plans=new ArrayList<>();
        for(var raw:nodes) {
            ObjectNode node=((ObjectNode)raw).deepCopy();String planned=quark.localPath(node);node.remove("plannedRelativePath");String shareId=required(node,"shareId");ObjectNode owner=quark.share(shareId);
            String identity=PathPlanner.digest("quark:"+shareId+":"+text(node,"id")+":"+text(node,"version"));String path=paths.output(identity,planned);
            ObjectNode output=object("id",identity,"nodeId",node.path("id"),"relativePath",path,"trackIds",List.of(),"kind","FILE","sizeBytes",node.path("sizeBytes"));
            ObjectNode item=object("id",identity,"contentId",null,"shareId",shareId,"projectTitle",owner.path("title"),"partId",null,"nodeId",node.path("id"),"title",node.path("name"),"version",node.path("version"),"relativePath",path,"sizeBytes",node.path("sizeBytes"),"decision","DOWNLOAD","reasonCode","PENDING_DOWNLOAD","reason","尚未下载所选文件","actionLabel","下载所选文件","selectedTrackIds",List.of(),"mergePolicy","SEPARATE","outputs",List.of(output),"downloadState","NOT_REQUESTED","archiveState","NOT_REQUESTED");classify(item,node,node,target);items.add(item);
            ObjectNode payload=object("schemaVersion",1,"shareId",shareId,"nodes",List.of(node),"outputs",List.of(output),"archivePolicy",target.path("archivePolicy"));
            plans.add(object("itemId",identity,"key",planKey(item,target),"accountRef",owner.path("accountId"),"destination",object("rootId",target.path("rootId"),"rootPath",target.path("path"),"relativePath","Quark/"+text(owner,"shareFolderKey")),"payload",payload));
        }
        String id=id();ObjectNode preview=object("id",id,"status","READY","platform","QUARK","expiresAt",store.clock.instant().plusSeconds(600).toString(),"storageRevision",store.get("target","default").path("revision"),"target",target,"archivePolicy",target.path("archivePolicy"),"items",items,"summary",object("download",count(items,"DOWNLOAD"),"reuse",count(items,"REUSE"),"blocked",count(items,"BLOCKED"),"total",items.size()),"warnings",List.of());
        store.put("preview",id,preview);store.put("previewPlans",id,object("plans",plans));store.put("previewNodes",id,object("nodes",nodes));return object("previewId",id,"taskId",null);
    }
    public ObjectNode order(String id) throws SQLException {ObjectNode order=store.get("order",id);List<String> states=new ArrayList<>();for(String task:strings(order.path("taskIds")))states.add(text(store.get("task",task),"status"));boolean blocked=list(order.path("items")).stream().anyMatch(i->text(i,"decision").equals("BLOCKED"));String state=states.isEmpty()||states.stream().allMatch("SUCCEEDED"::equals)?blocked?"PARTIAL":"SUCCEEDED":states.stream().anyMatch(s->Set.of("FAILED","NEEDS_ACTION").contains(s))?"NEEDS_ACTION":states.stream().anyMatch("RUNNING"::equals)?"RUNNING":states.getFirst();order.put("status",state);return management.enrichOrder(order);}
}
