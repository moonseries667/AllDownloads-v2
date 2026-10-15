package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.sql.SQLException;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** Logical consumers and immutable project manifests over the one existing operation queue. */
public final class DownloadManagement {
    private static final Set<String> TERMINAL=Set.of("SUCCEEDED","CANCELLED","FAILED");
    private final BusinessStore store;
    private final OperationQueue queue;
    private final StorageSettings settings;
    public DownloadManagement(BusinessStore store,OperationQueue queue,StorageSettings settings) {this.store=store;this.queue=queue;this.settings=settings;}

    public List<String> registerOrder(ObjectNode order) throws SQLException {
        if(order.path("projectIds").isArray())return strings(order.path("projectIds"));
        String platform=text(order,"platform");if(platform.isBlank())platform="BILIBILI";
        List<String> projects=registerBatch(text(order,"id"),platform,order.path("target"),list(order.path("items")),text(order,"createdAt"));
        order.set("projectIds",MAPPER.valueToTree(projects));store.put("order",text(order,"id"),order);return projects;
    }
    /** Items must have contentId (video, logical message, or share) and original outputs. */
    public List<String> registerBatch(String batchId,String platform,JsonNode target,List<JsonNode> items,String createdAt) throws SQLException {
        Map<String,List<JsonNode>> groups=new LinkedHashMap<>();
        for(JsonNode item:items) {String content=text(item,"contentId");if(content.isBlank())content=text(item,"shareId");if(content.isBlank())content=text(item,"resourceId");if(content.isBlank())throw Failure.conflict("MISSING_PROJECT_IDENTITY");groups.computeIfAbsent(content,k->new ArrayList<>()).add(item);}
        List<String> ids=new ArrayList<>();
        for(var entry:groups.entrySet()) {
            String projectId=PathPlanner.digest("project:"+batchId+":"+platform+":"+entry.getKey());ids.add(projectId);
            if(store.optional("downloadProject",projectId)!=null)continue;
            List<JsonNode> children=new ArrayList<>();
            for(JsonNode item:entry.getValue()) {
                String childId=PathPlanner.digest(projectId+":"+required(item,"id"));Set<String> tasks=new LinkedHashSet<>();
                for(JsonNode output:item.path("outputs")) {
                    ObjectNode existing=store.optional("file",text(target,"rootId")+":"+text(output,"id"));
                    if(existing!=null&&text(existing,"localState").equals("VERIFIED"))continue;
                    String[] claim=store.row("SELECT task_id FROM output_claims WHERE root_id=? AND output_id=?",text(target,"rootId"),text(output,"id"));if(claim!=null)tasks.add(claim[0]);
                }
                if(item.path("taskIds").isArray())tasks.addAll(strings(item.path("taskIds")));
                children.add(object("id",childId,"projectId",projectId,"contentId",entry.getKey(),"partId",nullable(item,"partId"),"attachmentId",nullable(item,"attachmentId"),"nodeId",nullable(item,"nodeId"),
                    "title",item.path("title"),"desiredAction","CONTINUE","taskIds",tasks,"outputs",item.path("outputs"),"blocked",text(item,"decision").equals("BLOCKED"),"detached",false));
            }
            String title=entry.getValue().getFirst().path("projectTitle").asText(text(entry.getValue().getFirst(),"title"));
            if(platform.equals("BILIBILI")) {ObjectNode content=store.optional("content",entry.getKey());if(content!=null)title=text(content,"title");}
            store.put("downloadProject",projectId,object("id",projectId,"batchId",batchId,"platform",platform,"contentId",entry.getKey(),"title",title,
                "createdAt",createdAt,"target",target,"archivePolicy",target.path("archivePolicy"),"archiveTarget",settings.frozenArchiveTarget(),"desiredAction","CONTINUE","items",children));
        }
        promote();return ids;
    }
    private static JsonNode nullable(JsonNode item,String field) {return item.hasNonNull(field)?item.path(field):MAPPER.nullNode();}
    private List<JsonNode> files(JsonNode project,JsonNode child) throws SQLException {
        List<JsonNode> result=new ArrayList<>();
        for(JsonNode output:child.path("outputs")) {
            ObjectNode record=store.optional("file",text(project.path("target"),"rootId")+":"+text(output,"id"));
            if(record!=null&&text(record,"localState").equals("VERIFIED")&&text(record.path("evidence"),"relativePath").equals(text(output,"relativePath")))result.add(record.path("evidence"));
        }
        return result;
    }
    private ObjectNode child(JsonNode project,JsonNode raw) throws SQLException {
        List<JsonNode> files=files(project,raw);List<String> states=new ArrayList<>();boolean executing=false;
        for(String id:strings(raw.path("taskIds"))) {ObjectNode task=store.get("task",id);states.add(text(task,"status"));executing|=Set.of("RUNNING","PAUSING","NEEDS_ACTION").contains(text(task,"status"));}
        String state;
        if(raw.path("detached").asBoolean())state="CANCELLED";
        else if(!raw.path("outputs").isEmpty()&&files.size()==raw.path("outputs").size()&&states.stream().allMatch("SUCCEEDED"::equals))state="SUCCEEDED";
        else if(raw.path("blocked").asBoolean())state="NEEDS_ACTION";
        else if(text(raw,"desiredAction").equals("CANCEL")&&!executing)state="CANCELLED";
        else if(states.contains("NEEDS_ACTION"))state="NEEDS_ACTION";
        else if(states.contains("RUNNING")||states.contains("PAUSING"))state="RUNNING";
        else if(states.contains("FAILED"))state="FAILED";
        else if(!states.isEmpty()&&states.stream().allMatch("CANCELLED"::equals))state="CANCELLED";
        else if(states.contains("WAITING_RETRY"))state="WAITING_RETRY";
        else if(states.contains("PAUSED"))state="PAUSED";
        else state="QUEUED";
        boolean cancellable=!TERMINAL.contains(state)&&!text(raw,"desiredAction").equals("CANCEL");
        ObjectNode view=object("id",raw.path("id"),"projectId",raw.path("projectId"),"contentId",raw.path("contentId"),"partId",raw.path("partId"),"attachmentId",raw.path("attachmentId"),"nodeId",raw.path("nodeId"),
            "title",raw.path("title"),"status",state,"desiredAction",raw.path("desiredAction"),"allowedActions",cancellable?List.of("CANCEL"):List.of(),
            "actionReason",cancellable?null:text(raw,"desiredAction").equals("CANCEL")&&!TERMINAL.contains(state)?"等待执行程序确认停止":raw.path("detached").asBoolean()?"已取消本次请求，共享执行仍供其他项目使用":"该部分已结束",
            "taskIds",raw.path("taskIds"),"outputs",raw.path("outputs"),"files",files);
        view.put("revision",PathPlanner.digest(canonical(view)));return view;
    }
    public ObjectNode project(String id) throws SQLException {
        ObjectNode raw=store.get("downloadProject",id);List<JsonNode> items=new ArrayList<>();for(JsonNode c:raw.path("items"))items.add(child(raw,c));
        int completed=count(items,"SUCCEEDED"),cancelled=count(items,"CANCELLED"),running=count(items,"RUNNING"),failed=count(items,"FAILED")+count(items,"NEEDS_ACTION");
        String state=completed==items.size()?"SUCCEEDED":cancelled==items.size()?"CANCELLED":running>0?"RUNNING":failed>0?"NEEDS_ACTION":completed+cancelled==items.size()?"PARTIAL":completed+cancelled>0?"PARTIAL":items.stream().anyMatch(i->text(i,"status").equals("WAITING_RETRY"))?"WAITING_RETRY":items.stream().allMatch(i->text(i,"status").equals("PAUSED"))?"PAUSED":"QUEUED";
        boolean cancellable=items.stream().anyMatch(i->strings(i.path("allowedActions")).contains("CANCEL"));
        ObjectNode view=object("id",id,"batchId",raw.path("batchId"),"platform",raw.path("platform"),"contentId",raw.path("contentId"),"title",raw.path("title"),"createdAt",raw.path("createdAt"),
            "target",raw.path("target"),"archivePolicy",raw.path("archivePolicy"),"archiveTarget",raw.path("archiveTarget"),"status",state,"desiredAction",raw.path("desiredAction"),
            "allowedActions",cancellable?List.of("CANCEL"):List.of(),"actionReason",cancellable?null:text(raw,"desiredAction").equals("CANCEL")&&running+failed>0?"等待执行程序确认停止":"项目没有可取消的部分",
            "summary",object("total",items.size(),"completed",completed,"running",running,"failed",failed,"cancelled",cancelled,"pending",items.size()-completed-running-failed-cancelled),"items",items);
        view.put("revision",PathPlanner.digest(canonical(view)));return view;
    }
    private static int count(List<JsonNode> values,String status) {return (int)values.stream().filter(v->text(v,"status").equals(status)).count();}
    public ObjectNode projects(Map<String,String> query) throws SQLException {
        List<JsonNode> out=new ArrayList<>();for(JsonNode raw:store.all("downloadProject"))if(matches(raw,query))out.add(project(text(raw,"id")));
        out.sort(Comparator.<JsonNode,String>comparing(p->text(p,"createdAt")).reversed().thenComparing(p->text(p,"id")));
        String revision=PathPlanner.digest(out.stream().map(p->text(p,"id")).reduce("",(a,b)->a+":"+b));return CatalogService.page(out,query,revision);
    }
    public boolean orderMatches(JsonNode order,Map<String,String> query) throws SQLException {
        if(!order.path("projectIds").isArray())return false;
        for(String id:strings(order.path("projectIds")))if(matches(store.get("downloadProject",id),query))return true;return false;
    }
    public ObjectNode enrichOrder(ObjectNode order) throws SQLException {
        List<JsonNode> projects=new ArrayList<>();for(JsonNode id:order.path("projectIds"))projects.add(project(id.asText()));order.set("projects",array(projects));
        if(!projects.isEmpty()) {
            Set<String> states=new HashSet<>();projects.forEach(p->states.add(text(p,"status")));
            order.put("status",states.size()==1?states.iterator().next():states.contains("RUNNING")?"RUNNING":states.contains("NEEDS_ACTION")?"NEEDS_ACTION":"PARTIAL");
        }
        return order;
    }
    public ObjectNode orders(Map<String,String> query) throws SQLException {
        List<JsonNode> out=new ArrayList<>();for(JsonNode order:store.all("order"))if((!query.containsKey("contentId")&&!query.containsKey("platform"))||orderMatches(order,query))out.add(enrichOrder((ObjectNode)order));
        out.sort(Comparator.<JsonNode,String>comparing(v->text(v,"createdAt")).reversed().thenComparing(v->text(v,"id")));
        return CatalogService.page(out,query,PathPlanner.digest(out.stream().map(v->text(v,"id")).reduce("",(a,b)->a+":"+b)));
    }
    private static boolean matches(JsonNode raw,Map<String,String> query) {return (!query.containsKey("platform")||text(raw,"platform").equals(query.get("platform")))&&(!query.containsKey("contentId")||text(raw,"contentId").equals(query.get("contentId")));}
    public ObjectNode cancel(String projectId,String itemId,String revision) throws SQLException {
        ObjectNode raw=store.get("downloadProject",projectId),view=project(projectId);JsonNode selected=view;
        if(itemId!=null)selected=list(view.path("items")).stream().filter(i->text(i,"id").equals(itemId)).findFirst().orElseThrow(Failure::missing);
        if(text(selected,"desiredAction").equals("CANCEL"))return (ObjectNode)selected;
        if(!text(selected,"revision").equals(revision))throw Failure.conflict("VERSION_CHANGED");
        if(!strings(selected.path("allowedActions")).contains("CANCEL"))return (ObjectNode)selected;
        List<String> cancelTasks=new ArrayList<>();Set<String> cancellingChildren=new HashSet<>();
        for(JsonNode item:raw.path("items"))if(itemId==null||text(item,"id").equals(itemId)) {
            ObjectNode current=child(raw,item);if(TERMINAL.contains(text(current,"status")))continue;
            ((ObjectNode)item).put("desiredAction","CANCEL");cancellingChildren.add(text(item,"id"));cancelTasks.addAll(strings(item.path("taskIds")));
        }
        if(itemId==null)raw.put("desiredAction","CANCEL");store.put("downloadProject",projectId,raw);
        Set<String> shared=new HashSet<>();
        for(String taskId:new LinkedHashSet<>(cancelTasks)) {
            boolean other=false;
            for(JsonNode p:store.all("downloadProject"))for(JsonNode c:p.path("items"))if(!text(c,"desiredAction").equals("CANCEL")&&strings(c.path("taskIds")).contains(taskId)&&!TERMINAL.contains(text(child(p,c),"status")))other=true;
            if(other){shared.add(taskId);continue;}
            ObjectNode task=store.get("task",taskId);
            if(strings(task.path("allowedActions")).contains("CANCEL"))queue.control(taskId,text(task,"revision"),"CANCEL");
            else if(text(task,"status").equals("NEEDS_ACTION"))queue.requestCancelUncertain(taskId);
        }
        for(JsonNode item:raw.path("items"))if(cancellingChildren.contains(text(item,"id"))&&!strings(item.path("taskIds")).isEmpty()&&shared.containsAll(strings(item.path("taskIds"))))((ObjectNode)item).put("detached",true);
        store.put("downloadProject",projectId,raw);ObjectNode after=project(projectId);
        return itemId==null?after:(ObjectNode)list(after.path("items")).stream().filter(i->text(i,"id").equals(itemId)).findFirst().orElseThrow();
    }
    /** Called in the same result transaction, after operation state has been persisted. */
    public void promote() throws SQLException {
        for(JsonNode raw:store.all("downloadProject")) {
            String id=text(raw,"id");if(!text(raw,"archivePolicy").equals("AUTO")||store.optional("archive",id)!=null)continue;
            ObjectNode project=project(id);
            if(!text(project,"status").equals("SUCCEEDED")||text(raw,"desiredAction").equals("CANCEL")||list(raw.path("items")).stream().anyMatch(i->text(i,"desiredAction").equals("CANCEL")))continue;
            if(!raw.path("archiveTarget").isObject()) {queue.issue(null,id,"ARCHIVE_TARGET_NOT_CONFIGURED","WARNING");continue;}
            Map<String,JsonNode> unique=new LinkedHashMap<>();for(JsonNode item:project.path("items"))for(JsonNode file:item.path("files"))unique.put(text(file,"outputId"),file);
            List<JsonNode> files=new ArrayList<>(unique.values());JsonNode local=raw.path("target");
            String task=queue.enqueue("UPLOAD_ARTIFACT",text(raw,"platform"),"archive-project:"+id,object("rootId",local.path("rootId"),"rootPath",local.path("path"),"relativePath",""),text(local,"revision"),
                object("schemaVersion",1,"projectId",id,"archivePolicy","AUTO","archiveTarget",raw.path("archiveTarget"),"files",files));
            store.put("archive",id,object("id",id,"projectId",id,"batchId",raw.path("batchId"),"platform",raw.path("platform"),"title",raw.path("title"),"target",raw.path("archiveTarget"),"files",files,"taskId",task,"createdAt",store.now()));
        }
    }
    public ObjectNode archives(Map<String,String> query) throws SQLException {
        List<JsonNode> out=new ArrayList<>();for(JsonNode value:store.all("archive")) {
            if(query.containsKey("platform")&&!text(value,"platform").equals(query.get("platform")))continue;
            ObjectNode view=(ObjectNode)value.deepCopy(),task=store.get("task",text(value,"taskId"));
            for(String field:List.of("status","revision","desiredAction","allowedActions","progress","error"))view.set(field,task.path(field));
            if(!query.containsKey("status")||text(view,"status").equals(query.get("status")))out.add(view);
        }
        out.sort(Comparator.<JsonNode,String>comparing(v->text(v,"createdAt")).reversed().thenComparing(v->text(v,"id")));
        return CatalogService.page(out,query,PathPlanner.digest(out.stream().map(v->text(v,"id")).reduce("",(a,b)->a+":"+b)));
    }
    public void uploadResult(JsonNode op,JsonNode result) throws SQLException {
        JsonNode payload=op.path("payload");String project=required(payload,"projectId");
        if(!project.equals(required(result,"projectId"))||!payload.path("archiveTarget").equals(result.path("archiveTarget"))||!result.path("verified").asBoolean(false)||!result.path("files").isArray()||result.path("files").size()!=payload.path("files").size())throw Failure.conflict("VERIFICATION_FAILED");
        Map<String,JsonNode> expected=new HashMap<>();for(JsonNode file:payload.path("files"))expected.put(text(file,"outputId"),file);
        Set<String> remoteIds=new HashSet<>();for(JsonNode file:result.path("files")) {
            JsonNode known=expected.remove(required(file,"outputId"));if(known==null||!file.path("verified").asBoolean(false)||!remoteIds.add(required(file,"remoteId")))throw Failure.conflict("VERIFICATION_FAILED");
            for(String field:List.of("relativePath","sizeBytes","sha256"))if(!required(file,field).equals(text(known,field)))throw Failure.conflict("VERIFICATION_FAILED");
            String path=text(known,"relativePath");if(!required(file,"remoteName").equals(path.substring(path.lastIndexOf('/')+1)))throw Failure.conflict("VERIFICATION_FAILED");
        }
        if(!expected.isEmpty())throw Failure.conflict("VERIFICATION_FAILED");
        ObjectNode archive=store.get("archive",project);archive.set("files",result.path("files"));store.put("archive",project,archive);
        for(JsonNode evidence:payload.path("files")) {String key=text(op.path("destination"),"rootId")+":"+text(evidence,"outputId");ObjectNode file=store.get("file",key);file.put("archiveState","VERIFIED");store.put("file",key,file);}
    }
}
