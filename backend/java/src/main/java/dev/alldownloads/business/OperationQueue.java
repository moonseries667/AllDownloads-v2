package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.sql.SQLException;
import java.time.Instant;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** One persistent queue for metadata, downloads and archive stages; called within the store transaction. */
public final class OperationQueue {
    private final BusinessStore store;
    @FunctionalInterface public interface Completion { void accept(JsonNode operation,JsonNode result) throws Exception; }
    private Completion completion=(op,result)->{};
    private Completion outcome=(op,result)->{};
    public void onOutcome(Completion value) { outcome=value; }
    @FunctionalInterface public interface Report { void accept(JsonNode operation,JsonNode request,String status) throws Exception; }
    private Report report=(op,request,status)->{};
    public OperationQueue(BusinessStore store) { this.store=store; }
    public void onCompletion(Completion value) { completion=value; }
    public void onReport(Report value) { report=value; }
    public void account(String taskId,String accountId) throws SQLException {
        String[] r=store.row("SELECT id,body FROM operations WHERE task_id=?",taskId);if(r==null)throw Failure.missing();ObjectNode body=(ObjectNode)parse(r[1]);body.put("accountRef",accountId);store.update("UPDATE operations SET body=? WHERE id=?",body.toString(),r[0]);
    }
    public String enqueue(String type,String platform,String key,JsonNode destination,String storageRevision,JsonNode payload) throws SQLException {
        String[] prior=store.row("SELECT task_id FROM operations WHERE resource_key=? AND type=? AND status NOT IN ('SUCCEEDED','CANCELLED','FAILED') ORDER BY created LIMIT 1",key,type);
        if(prior!=null)return prior[0];
        String taskId=id(),operationId=id();
        ObjectNode task=object("id",taskId,"platform",platform,"kind",kind(type),"status","QUEUED","phase",type,"progress",null,"desiredAction","CONTINUE","allowedActions",List.of("PAUSE","CANCEL"),"error",null,"nextRetryAt",null,"createdAt",store.now(),"updatedAt",store.now(),"revision","1");
        ObjectNode op=object("protocolVersion",1,"operationId",operationId,"taskId",taskId,"operationType",type,"platform",platform,"accountRef",payload.path("accountId").isTextual()?text(payload,"accountId"):null,"resourceKey",key,"storageRevision",storageRevision,"destination",destination,"payload",payload);
        store.put("task",taskId,task);
        store.update("INSERT INTO operations(id,task_id,type,resource_key,body,status,created) VALUES(?,?,?,?,?,'QUEUED',?)",operationId,taskId,type,key,op.toString(),store.millis());
        return taskId;
    }
    private String kind(String type) { return switch(type) { case "DOWNLOAD_MEDIA"->"DOWNLOAD";case "UPLOAD_ARTIFACT"->"UPLOAD";case "VERIFY_ARCHIVE"->"VERIFY";case "TRANSFER_QUARK_ITEMS"->"TRANSFER_SYNC";default->"INFO_SYNC"; }; }
    public ObjectNode register(JsonNode request) throws SQLException {
        String runner=required(request,"runnerId");if(request.path("protocolVersion").asInt()!=1)throw Failure.conflict("PROTOCOL_MISMATCH");
        List<String> capabilities=strings(request.path("capabilities"));
        ObjectNode data=object("id",runner,"capabilities",capabilities,"tools",request.path("tools"),"facts",request.path("facts"),"lastSeenAt",store.now());
        store.put("runner",runner,data);return object("runnerId",runner,"protocolVersion",1,"heartbeatSeconds",15,"leaseSeconds",90);
    }
    public ObjectNode acquire(JsonNode request) throws SQLException {
        String runner=required(request,"runnerId");ObjectNode r=store.get("runner",runner);r.put("lastSeenAt",store.now());store.put("runner",runner,r);expire();
        Set<String> capabilities=new HashSet<>(strings(r.path("capabilities")));capabilities.retainAll(strings(request.path("capabilities")));
        for(String[] candidate:store.rows("SELECT id,type,task_id FROM operations WHERE status IN ('QUEUED','WAITING_RETRY') AND (retry_at IS NULL OR retry_at<=?) ORDER BY created,id",store.millis())) {
            if(!capabilities.contains(candidate[1]))continue;
            ObjectNode task=store.get("task",candidate[2]);if(!text(task,"desiredAction").equals("CONTINUE"))continue;
            String lease=id();store.update("UPDATE operations SET status='RUNNING',attempt=attempt+1,fence=fence+1,lease_id=?,runner=?,expires=?,seq=-1,retry_at=NULL WHERE id=?",lease,runner,store.millis()+90_000,candidate[0]);
            state(task,"RUNNING",null,null);return object("operation",lease(candidate[0]));
        }
        return object("operation",null);
    }
    private ObjectNode lease(String operationId) throws SQLException {
        String[] row=store.row("SELECT body,attempt,fence,lease_id,runner,expires FROM operations WHERE id=?",operationId);if(row==null)throw Failure.missing();
        ObjectNode op=(ObjectNode)parse(row[0]);op.put("attempt",Integer.parseInt(row[1]));op.put("fencingToken",row[2]);op.put("leaseId",row[3]);op.put("runnerId",row[4]);op.put("leaseExpiresAt",row[5]==null?null:Instant.ofEpochMilli(Long.parseLong(row[5])).toString());return op;
    }
    private ObjectNode proof(String operationId,JsonNode request,boolean reconcile) throws SQLException {
        ObjectNode op=lease(operationId);
        for(String key:List.of("runnerId","leaseId","fencingToken"))if(!text(op,key).equals(required(request,key)))throw Failure.conflict("STALE_LEASE");
        if(op.path("attempt").asInt()!=request.path("attempt").asInt())throw Failure.conflict("STALE_LEASE");
        String[] r=store.row("SELECT status,expires FROM operations WHERE id=?",operationId);
        boolean active=r[0].equals("RUNNING")&&r[1]!=null&&Long.parseLong(r[1])>store.millis();
        if(!active&&!(reconcile&&Set.of("RUNNING","NEEDS_ACTION").contains(r[0])))throw Failure.conflict("RECONCILE_REQUIRED");
        return op;
    }
    private ObjectNode prior(String event,String route,JsonNode body) throws SQLException {
        String[] r=store.row("SELECT body,ack FROM events WHERE id=?",event);if(r==null)return null;
        if(!r[0].equals(route+":"+canonical(body)))throw Failure.conflict("EVENT_CONFLICT");return (ObjectNode)parse(r[1]);
    }
    private ObjectNode ack(String event,String route,JsonNode body,ObjectNode ack) throws SQLException { store.update("INSERT INTO events VALUES(?,?,?)",event,route+":"+canonical(body),ack.toString());return ack; }
    public ObjectNode heartbeat(String leaseId,JsonNode request) throws SQLException {
        String event=required(request,"eventId"),operationId=required(request,"operationId"),route="heartbeat:"+leaseId;ObjectNode old=prior(event,route,request);if(old!=null)return old;
        ObjectNode op=proof(operationId,object("runnerId",request.path("runnerId"),"leaseId",leaseId,"attempt",request.path("attempt"),"fencingToken",request.path("fencingToken")),false);
        long sequence=request.path("sequence").asLong(-1);if(sequence<0)throw Failure.bad("INVALID_INPUT","Invalid sequence");
        store.update("UPDATE operations SET seq=?,expires=? WHERE id=? AND seq<?",sequence,store.millis()+90_000,operationId,sequence);
        ObjectNode runner=store.get("runner",text(op,"runnerId"));runner.put("lastSeenAt",store.now());store.put("runner",text(op,"runnerId"),runner);
        return ack(event,route,request,object("desiredAction",store.get("task",text(op,"taskId")).path("desiredAction"),"leaseExpiresAt",lease(operationId).path("leaseExpiresAt")));
    }
    public ObjectNode facts(String operationId,JsonNode request) throws SQLException {
        String event=required(request,"eventId"),route="facts:"+operationId;ObjectNode old=prior(event,route,request);if(old!=null)return old;
        proof(operationId,request,false);if(!request.path("facts").isObject())throw Failure.bad("INVALID_INPUT","facts required");
        // Facts are durable but only the final verified result may publish catalog changes or success.
        return ack(event,route,request,object("ackId",id(),"eventId",event));
    }
    public ObjectNode result(String operationId,JsonNode request,boolean reconcile) throws Exception {
        String event=required(request,"eventId"),route="result:"+operationId;ObjectNode old=prior(event,route,request);if(old!=null)return old;
        ObjectNode op=proof(operationId,request,reconcile),task=store.get("task",text(op,"taskId"));String status=required(request,"status"),errorCode=null;Long retry=null;
        switch(status) {
            case "SUCCEEDED" -> {
                if(!request.path("result").isObject())throw Failure.bad("INVALID_INPUT","Result facts required");
                completion.accept(op,request.path("result"));
                resolveIssues(text(op,"taskId"));
            }
            case "PAUSED","CANCELLED" -> {
                if(request.path("result").path("files").size()>0&&!text(op,"operationType").equals("DOWNLOAD_MEDIA"))throw Failure.conflict("EFFECTS_REQUIRE_RECONCILIATION");
                if(status.equals("PAUSED")&&text(task,"desiredAction").equals("CANCEL"))status="CANCELLED";
                task.put("desiredAction",status.equals("PAUSED")?"PAUSE":"CANCEL");
            }
            case "RETRYABLE_ERROR","NEEDS_ACTION","RESULT_UNKNOWN" -> {
                errorCode=required(request.path("error"),"code");
                if(status.equals("RETRYABLE_ERROR")&&!text(op,"operationType").equals("SELECT_LOCAL_DIRECTORY")&&request.path("error").path("completedEffects").size()==0&&request.path("error").path("pendingEffects").size()==0) {
                    if(text(task,"desiredAction").equals("CANCEL"))status="CANCELLED";
                    else if(text(task,"desiredAction").equals("PAUSE"))status="PAUSED";
                    else if(op.path("attempt").asInt()<=3) { status="WAITING_RETRY";retry=store.millis()+Math.max(new long[]{30_000,120_000,600_000}[op.path("attempt").asInt()-1],Math.min(86_400L,Math.max(0,request.path("error").path("retryAfterSeconds").asLong(0)))*1000); }
                    else status="FAILED";
                } else status="NEEDS_ACTION";
                issue(text(op,"taskId"),text(op,"resourceKey"),errorCode,"ERROR");
                if(text(op,"operationType").equals("SYNC_BIL_SOURCE")) {
                    ObjectNode source=store.get("source",text(op.path("payload"),"sourceId"));source.put("status",status.equals("WAITING_RETRY")?"WAITING_RETRY":"NEEDS_ACTION");store.put("source",text(source,"id"),source);
                }
                if(text(op,"operationType").equals("VALIDATE_TARGET")) {
                    ObjectNode target=store.get("target",text(op.path("payload"),"targetId"));target.put("status","INVALID");target.set("error",object("code",errorCode,"message",message(errorCode)));store.put("target",text(target,"id"),target);
                }
            }
            default -> throw Failure.bad("INVALID_INPUT","Unsupported result status");
        }
        report.accept(op,request,status);
        store.update("UPDATE operations SET status=?,retry_at=? WHERE id=?",status,retry,operationId);state(task,status,errorCode,retry);
        outcome.accept(op,request);
        return ack(event,route,request,object("ackId",id(),"eventId",event,"taskId",text(task,"id"),"status",status));
    }
    public void expire() throws SQLException {
        for(String[] r:store.rows("SELECT id,task_id,resource_key FROM operations WHERE status='RUNNING' AND expires<=?",store.millis())) {
            store.update("UPDATE operations SET status='NEEDS_ACTION' WHERE id=?",r[0]);state(store.get("task",r[1]),"NEEDS_ACTION","RESULT_UNKNOWN",null);issue(r[1],r[2],"RESULT_UNKNOWN","ERROR");
        }
    }
    public ObjectNode control(String id,String revision,String action) throws SQLException {
        ObjectNode task=store.get("task",id);if(!text(task,"revision").equals(revision))throw Failure.conflict("VERSION_CHANGED");
        if(!strings(task.path("allowedActions")).contains(action))throw Failure.conflict("ACTION_NOT_ALLOWED");
        String[] op=store.row("SELECT id,status FROM operations WHERE task_id=?",id);
        switch(action) {
            case "PAUSE","CANCEL" -> {
                task.put("desiredAction",action);boolean running=op[1].equals("RUNNING");String status=running?(action.equals("PAUSE")?"PAUSING":"RUNNING"):(action.equals("PAUSE")?"PAUSED":"CANCELLED");
                if(!running)store.update("UPDATE operations SET status=? WHERE id=?",status,op[0]);state(task,status,null,null);
            }
            case "RESUME","RETRY" -> {
                JsonNode plan=parse(store.row("SELECT body FROM operations WHERE id=?",op[0])[0]);
                if(text(plan,"operationType").equals("DOWNLOAD_MEDIA"))for(JsonNode output:plan.path("payload").path("outputs")) {
                    String[] claim=store.row("SELECT task_id FROM output_claims WHERE root_id=? AND output_id=?",text(plan.path("destination"),"rootId"),text(output,"id"));
                    if(claim!=null&&!claim[0].equals(id))throw Failure.conflict("OUTPUT_OWNED_BY_ANOTHER_TASK");
                }
                task.put("desiredAction","CONTINUE");store.update("UPDATE operations SET status='QUEUED',retry_at=NULL WHERE id=?",op[0]);state(task,"QUEUED",null,null);
            }
            case "RECONCILE" -> { /* Keep retained proof: the runner must reconcile its durable receipt. */ }
            default -> throw Failure.conflict("ACTION_NOT_ALLOWED");
        }
        return task;
    }
    public void requestCancelUncertain(String id) throws SQLException {
        ObjectNode task=store.get("task",id);task.put("desiredAction","CANCEL");state(task,"NEEDS_ACTION","RESULT_UNKNOWN",null);
    }
    private void state(ObjectNode task,String status,String error,Long retry) throws SQLException {
        task.put("status",status);task.put("revision",Long.toString(Long.parseLong(text(task,"revision"))+1));task.put("updatedAt",store.now());task.set("error",error==null?null:object("code",error,"message",message(error)));task.put("nextRetryAt",retry==null?null:Instant.ofEpochMilli(retry).toString());
        List<String> allowed=switch(status) {case "QUEUED","WAITING_RETRY","RUNNING"->List.of("PAUSE","CANCEL");case "PAUSED"->List.of("RESUME","CANCEL");case "FAILED"->List.of("RETRY","CANCEL");case "NEEDS_ACTION"->List.of("RECONCILE");default->List.of();};
        task.set("allowedActions",MAPPER.valueToTree(allowed));store.put("task",text(task,"id"),task);
    }
    public void issue(String taskId,String objectId,String code,String severity) throws SQLException {
        new dev.alldownloads.feedback2.IssueRepository(store.domainConnection(),MAPPER,store.clock).record(taskId,objectId,code,severity);
    }
    private void resolveIssues(String taskId) throws SQLException {new dev.alldownloads.feedback2.IssueRepository(store.domainConnection(),MAPPER,store.clock).taskSucceeded(taskId);}
    /** Only explicitly requested retries with persisted, known file effects may bypass reconciliation. */
    public ObjectNode retryKnownDownload(String taskId) throws SQLException {
        ObjectNode task=store.get("task",taskId);String status=text(task,"status");
        if(status.equals("FAILED"))return control(taskId,text(task,"revision"),"RETRY");
        if(status.equals("PAUSED"))return control(taskId,text(task,"revision"),"RESUME");
        if(!status.equals("NEEDS_ACTION")||store.optional("knownDownloadFailure",taskId)==null)throw Failure.conflict("RECONCILE_REQUIRED");
        task.set("allowedActions",MAPPER.valueToTree(List.of("RETRY")));store.put("task",taskId,task);return control(taskId,text(task,"revision"),"RETRY");
    }
    public static String message(String code) { return switch(code) {case "RESOURCE_DELETED"->"原资源已确认删除";case "PERMISSION_DENIED","ACCOUNT_PERMISSION_DENIED"->"没有访问权限";case "REGION_RESTRICTED"->"当前地区不可访问";case "PAYMENT_REQUIRED"->"内容需要相应付费权限";case "NETWORK_TEMPORARY"->"网络暂时失败，可稍后重试";case "RESULT_UNKNOWN"->"执行结果待核对";case "INVALID_STORAGE_ROOT"->"下载目录无效或不可写";case "ACCOUNT_NOT_CONFIGURED","AUTH_REQUIRED"->"尚未配置可用账号";case "PROVIDER_CHANGED"->"平台接口或内容已改变，需要重新获取资料";default->"处理未完成（"+code.replaceAll("[^A-Z0-9_]","")+"）";}; }
}
