package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.feedback2.*;
import java.sql.SQLException;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** Part-two adapters into the existing transaction, queue, downloads and issue records. */
public final class FeedbackService {
    private final BusinessStore store;
    private final OperationQueue queue;
    private final DownloadService downloads;
    public final TelegramLibrary telegram;
    public final QuarkCatalog quark;
    public final IssueRepository issues;
    public FeedbackService(BusinessStore store,OperationQueue queue,DownloadService downloads) {
        this.store=store;this.queue=queue;this.downloads=downloads;
        issues=new IssueRepository(store.domainConnection(),MAPPER,store.clock);
        telegram=new TelegramLibrary(store.domainConnection(),MAPPER,store.clock,issues);quark=new QuarkCatalog(store.domainConnection(),MAPPER,store.clock);
        store.tx(()->{issues.initialize();telegram.initialize();quark.initialize();return null;});downloads.useQuark(quark);
    }
    private String enqueue(String type,String platform,String account,String key,ObjectNode payload) throws SQLException {
        if(type.equals("DOWNLOAD_MEDIA")) {String resource=required(payload,"resourceId");String task=downloads.savedDownload(key,account,telegram.batchId(resource),payload);telegram.bindTasks(resource,downloads.savedAssignments(task));return task;}
        String task=queue.enqueue(type,platform,key,null,"1",payload);queue.account(task,account);return task;
    }
    public ObjectNode savedSync(JsonNode request) throws SQLException {
        String account=required(request,"accountId");String active=telegram.activeTask(account);
        if(active!=null) {
            String state=text(store.get("task",active),"status");if(!Set.of("CANCELLED","FAILED","SUCCEEDED").contains(state))return object("taskId",active,"reused",true);
            telegram.scanStopped(active);
        }
        downloads.defaultTarget();String task=enqueue("SCAN_TEL_SAVED","TELEGRAM",account,"tel-scan:"+account+":"+id(),object("schemaVersion",1,"accountId",account,"afterMessageId",telegram.watcher(account).path("scanState").path("cursorMessageId"),"continuation",null));
        telegram.beginScan(account,task);return object("taskId",task,"reused",false);
    }
    public ObjectNode retryIncomplete(JsonNode request) throws SQLException {
        String account=required(request,"accountId");Set<String> selected=request.has("resourceIds")?new HashSet<>(strings(request.path("resourceIds"))):null;
        List<String> taskIds=telegram.incompleteTasks(account,selected);List<JsonNode> items=new ArrayList<>();
        Map<String,JsonNode> newInstances=new LinkedHashMap<>();
        for(String taskId:taskIds) {
            ObjectNode task=store.get("task",taskId);String state=text(task,"status");
            if(Set.of("FAILED","PAUSED","NEEDS_ACTION").contains(state))queue.retryKnownDownload(taskId);
            else if(state.equals("CANCELLED")) {
                JsonNode manifest=telegram.payloadForTask(taskId);newInstances.put(text(manifest,"resourceId"),manifest);
                String[] r=store.row("SELECT body FROM operations WHERE task_id=?",taskId);ObjectNode op=(ObjectNode)parse(r[0]);
                String next=queue.enqueue("DOWNLOAD_MEDIA","TELEGRAM",text(op,"resourceKey"),op.path("destination"),text(op,"storageRevision"),op.path("payload"));queue.account(next,account);store.put("taskTarget",next,store.get("taskTarget",taskId));telegram.replaceTask(taskId,next);taskId=next;
                for(JsonNode output:op.path("payload").path("outputs"))store.update("UPDATE output_claims SET task_id=? WHERE root_id=? AND output_id=?",next,text(op.path("destination"),"rootId"),text(output,"id"));
            }
            items.add(object("taskId",taskId));
        }
        String retryBatch=id();for(JsonNode manifest:newInstances.values())downloads.registerSavedInstance(retryBatch,manifest);
        return object("items",items,"taskIds",items.stream().map(n->text(n,"taskId")).toList());
    }
    public ObjectNode folders(Map<String,String> query) throws SQLException {
        String account=query.get("accountId");if(account==null||account.isBlank())throw Failure.bad("INVALID_INPUT","accountId is required");
        String parent=query.get("parentId");ObjectNode result=quark.folders(account,parent);
        if(text(result,"status").equals("LOADING")) {
            String task=enqueue("LIST_QUARK_TREE","QUARK",account,"quark-tree:"+account+":"+parent,object("schemaVersion",1,"accountId",account,"parentId",parent,"shareId",null));result.put("taskId",task);
            String status=text(store.get("task",task),"status");if(Set.of("NEEDS_ACTION","FAILED").contains(status))result.put("status","NEEDS_ACTION");
        }
        ObjectNode page=CatalogService.page(list(result.path("items")),query,PathPlanner.digest(canonical(result.path("items"))));result.set("items",page.path("items"));result.set("nextCursor",page.path("nextCursor"));return result;
    }
    public ObjectNode addShares(JsonNode request) throws SQLException {if(!request.isObject())throw Failure.bad("INVALID_INPUT","request required");return quark.addShares((ObjectNode)request,this::enqueue);}
    public ObjectNode refreshTree(String shareId) throws SQLException {ObjectNode share=quark.share(shareId);String account=text(share,"accountId");String task=enqueue("LIST_QUARK_TREE","QUARK",account,"quark-share-tree:"+shareId,object("schemaVersion",1,"accountId",account,"parentId",null,"shareId",shareId));return object("taskId",task);}
    public ObjectNode messages(Map<String,String> query) throws SQLException {
        List<JsonNode> values=list(telegram.messages(query.get("accountId")));String q=query.getOrDefault("q","").toLowerCase(Locale.ROOT);if(!q.isEmpty())values.removeIf(n->!text(n,"text").toLowerCase(Locale.ROOT).contains(q));
        String type=query.get("mediaType");if(type!=null)values.removeIf(n->list(n.path("attachments")).stream().noneMatch(a->text(a,"mediaType").equals(type)));
        values.sort(Comparator.comparing((JsonNode n)->text(n,"sentAt")).reversed().thenComparing(n->text(n,"id")));return CatalogService.page(values,query,PathPlanner.digest(canonical(array(values))));
    }
    public boolean complete(JsonNode op,JsonNode result) throws SQLException {
        String type=text(op,"operationType"),platform=text(op,"platform"),task=text(op,"taskId");
        if(type.equals("SCAN_TEL_SAVED")) {if(!text(op.path("payload"),"accountId").equals(required(result,"accountId")))throw Failure.conflict("VERIFICATION_FAILED");telegram.acceptPage(task,text(op,"operationId")+":"+op.path("attempt").asInt(),(ObjectNode)result,this::enqueue);return true;}
        if(type.equals("LIST_QUARK_TREE")) {if(!text(op.path("payload"),"accountId").equals(required(result,"accountId"))||!op.path("payload").path("parentId").equals(result.path("parentId")))throw Failure.conflict("VERIFICATION_FAILED");ObjectNode tree=((ObjectNode)result).deepCopy();if(op.path("payload").hasNonNull("shareId"))tree.set("shareId",op.path("payload").path("shareId"));quark.acceptTree(tree);return true;}
        if(type.equals("TRANSFER_QUARK_ITEMS")) {if(!text(op.path("payload"),"shareId").equals(required(result,"shareId")))throw Failure.conflict("VERIFICATION_FAILED");quark.transferResult((ObjectNode)result);refreshTree(text(result,"shareId"));return true;}
        if(type.equals("DOWNLOAD_MEDIA")&&platform.equals("TELEGRAM")) {telegram.downloadFacts(task,result.path("files"),true);downloads.downloadResult(op,result);return true;}
        return false;
    }
    public void reported(JsonNode op,JsonNode request,String status) throws SQLException {
        String task=text(op,"taskId");
        if(text(op,"operationType").equals("SCAN_TEL_SAVED")&&Set.of("FAILED","CANCELLED","NEEDS_ACTION").contains(status))telegram.scanStopped(task);
        if(text(op,"operationType").equals("DOWNLOAD_MEDIA")&&text(op,"platform").equals("TELEGRAM")&&!status.equals("SUCCEEDED")) {
            JsonNode files=request.path("result").path("files");telegram.downloadFacts(task,files.isArray()?files:array(List.of()),false);
            if(!text(request,"status").equals("RESULT_UNKNOWN")&&!text(request.path("error"),"code").equals("RESULT_UNKNOWN")&&!Set.of("CANCELLED","PAUSED").contains(status))store.put("knownDownloadFailure",task,object("eventId",request.path("eventId"),"code",request.path("error").path("code")));
        }
    }
    public void tick() {store.tx(()->{for(String account:telegram.dueAccounts())try{savedSync(object("accountId",account));}catch(Failure e){issues.record(null,account,e.code,"WARNING");}return null;});}
}
