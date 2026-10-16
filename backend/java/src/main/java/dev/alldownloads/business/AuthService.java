package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.sql.SQLException;
import java.time.Instant;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** Public facts only. Platform identities, QR keys and OAuth credentials belong to Go. */
public final class AuthService {
    private static final Map<String,String> ACCOUNTS=Map.of("BILIBILI","bilibili-main","TELEGRAM","telegram-main","QUARK","quark-main","GOOGLE_DRIVE","drive-main");
    private static final Set<String> ACCOUNT_FIELDS=Set.of("id","platform","label","state","lastCheckedAt","reasonCode","revision");
    private static final Set<String> SESSION_FIELDS=Set.of("authSessionId","accountId","platform","state","nextAction","url","message","expiresAt","revision","taskId","reasonCode");
    private final BusinessStore store;
    private final OperationQueue queue;
    public AuthService(BusinessStore store,OperationQueue queue) {this.store=store;this.queue=queue;}

    public void initialize() throws SQLException {
        for(var entry:ACCOUNTS.entrySet())if(store.optional("account",entry.getValue())==null)
            store.put("account",entry.getValue(),object("id",entry.getValue(),"platform",entry.getKey(),"label",label(entry.getKey()),"state","UNKNOWN","lastCheckedAt",null,"reasonCode",null,"revision","1"));
        // An unfinished flow and its operation survive Java restarts with the same identity.
        expire();
    }
    public ObjectNode accounts() throws SQLException {return object("items",store.all("account"),"nextCursor",null);}
    public ObjectNode account(String id) throws SQLException {
        if(!ACCOUNTS.containsValue(id))throw Failure.missing();return store.get("account",id);
    }
    public ObjectNode check(String id) throws SQLException {
        ObjectNode account=account(id);
        String[] active=store.row("SELECT task_id FROM operations WHERE type='CHECK_ACCOUNT' AND json_extract(body,'$.payload.accountId')=? AND (status IN ('QUEUED','WAITING_RETRY') OR status='RUNNING' AND expires>?) ORDER BY created DESC LIMIT 1",id,store.millis());
        String task=active==null?queue.enqueue("CHECK_ACCOUNT",route(text(account,"platform")),"account-check:"+id+":"+id(),null,"1",object("schemaVersion",1,"accountId",id,"platform",account.path("platform"))):active[0];
        store.put("accountCheck",id,object("taskId",task));return object("accountId",id,"taskId",task);
    }
    public ObjectNode begin(JsonNode request) throws SQLException {
        fields(request,Set.of("platform"));String platform=required(request,"platform"),accountId=ACCOUNTS.get(platform);
        if(accountId==null)throw invalid();expire();
        for(JsonNode session:store.all("authSession"))if(text(session,"platform").equals(platform)&&open(session))return (ObjectNode)session;
        String id=id();ObjectNode session=object("authSessionId",id,"accountId",accountId,"platform",platform,"state","PENDING","nextAction","WAIT","url",null,"message","正在准备授权流程","expiresAt",store.clock.instant().plusSeconds(600).toString(),"revision","1","taskId",null,"reasonCode",null);
        enqueue(session,"BEGIN");return session;
    }
    public ObjectNode session(String id) throws SQLException {
        ObjectNode session=store.get("authSession",id);
        // GET remains pure even between the deadline and the periodic expiry transaction.
        if(open(session)&&expired(session)) {ended(session,"EXPIRED","AUTH_SESSION_EXPIRED");session.put("revision","expired-"+text(session,"revision"));}
        return session;
    }
    public ObjectNode poll(String id,JsonNode request) throws SQLException {
        fields(request,Set.of("action"));if(!required(request,"action").equals("POLL"))throw invalid();
        ObjectNode session=store.get("authSession",id);
        if(expired(session))throw Failure.conflict("AUTH_SESSION_EXPIRED");
        if(!open(session)||text(session,"nextAction").equals("NONE"))throw Failure.conflict("ACTION_NOT_ALLOWED");
        String[] pending=store.row("SELECT status FROM operations WHERE task_id=?",text(session,"taskId"));
        if(pending!=null&&Set.of("QUEUED","RUNNING","WAITING_RETRY","PAUSED").contains(pending[0]))return session;
        bump(session);enqueue(session,"POLL");return session;
    }
    public ObjectNode cancel(String id,String revision) throws SQLException {
        ObjectNode session=store.get("authSession",id);
        if(!text(session,"revision").equals(revision))throw Failure.conflict("VERSION_CHANGED");
        if(!open(session)||expired(session))throw Failure.conflict("ACTION_NOT_ALLOWED");
        stop(session);ended(session,"CANCELLED",null);enqueue(session,"CANCEL");return session;
    }
    private void enqueue(ObjectNode session,String action) throws SQLException {
        String task=queue.enqueue("AUTH_STEP",route(text(session,"platform")),"auth-step:"+text(session,"authSessionId")+":"+text(session,"revision")+":"+action,null,"1",
            object("schemaVersion",1,"authSessionId",session.path("authSessionId"),"accountId",session.path("accountId"),"platform",session.path("platform"),"action",action));
        session.put("taskId",task);store.put("authSession",text(session,"authSessionId"),session);
    }
    public void expire() throws SQLException {
        for(JsonNode value:store.all("authSession"))if(open(value)&&expired(value)) {
            ObjectNode session=(ObjectNode)value;stop(session);ended(session,"EXPIRED","AUTH_SESSION_EXPIRED");
            // Cleanup is durable, uses the same session, and cannot affect a saved account.
            enqueue(session,"CANCEL");
        }
    }
    private void stop(JsonNode session) throws SQLException {
        for(String[] row:store.rows("SELECT task_id FROM operations WHERE type='AUTH_STEP' AND json_extract(body,'$.payload.authSessionId')=? AND status NOT IN ('SUCCEEDED','CANCELLED','FAILED')",text(session,"authSessionId"))) {
            ObjectNode task=store.get("task",row[0]);
            if(strings(task.path("allowedActions")).contains("CANCEL"))queue.control(row[0],text(task,"revision"),"CANCEL");
            else queue.requestCancelUncertain(row[0]);
        }
    }
    public boolean complete(JsonNode operation,JsonNode result) throws SQLException {
        String type=text(operation,"operationType");if(!Set.of("CHECK_ACCOUNT","AUTH_STEP").contains(type))return false;
        identity(operation);
        if(type.equals("CHECK_ACCOUNT")) {
            fields(result,Set.of("account"));JsonNode value=result.path("account");factFields(value,ACCOUNT_FIELDS,Set.of("revision"));
            if(!value.path("label").isTextual()||text(value,"label").isEmpty()||text(value,"label").length()>120)throw invalid();
            ObjectNode account=account(text(operation.path("payload"),"accountId"));
            if(!text(value,"id").equals(text(account,"id"))||!text(value,"platform").equals(text(account,"platform")))throw invalid();
            String state=required(value,"state"),reason=reason(value);
            if(!Set.of("ACTIVE","AUTH_REQUIRED","UNKNOWN","ERROR").contains(state)||state.equals("ACTIVE")&&reason!=null||Set.of("AUTH_REQUIRED","ERROR").contains(state)&&reason==null)throw invalid();
            Instant checked=value.path("lastCheckedAt").isNull()?null:instant(value,"lastCheckedAt");
            if(state.equals("ACTIVE")&&checked==null||checked!=null&&checked.isAfter(store.clock.instant().plusSeconds(30)))throw invalid();
            ObjectNode latest=store.optional("accountCheck",text(account,"id"));
            if(latest!=null&&text(latest,"taskId").equals(text(operation,"taskId"))) {
                account.put("state",state);account.put("reasonCode",reason);account.put("lastCheckedAt",checked==null?null:checked.toString());bump(account);store.put("account",text(account,"id"),account);
            }
        } else {
            fields(result,Set.of("session"));JsonNode value=result.path("session");factFields(value,SESSION_FIELDS,Set.of("revision","taskId"));
            if(!value.path("message").isTextual()||text(value,"message").length()>1000)throw invalid();
            JsonNode payload=operation.path("payload");ObjectNode session=store.get("authSession",required(payload,"authSessionId"));
            for(String field:List.of("authSessionId","accountId","platform"))if(!required(value,field).equals(text(session,field)))throw invalid();
            String state=required(value,"state"),next=required(value,"nextAction"),reason=reason(value);
            if(!Set.of("PENDING","SUCCEEDED","CANCELLED","EXPIRED","NEEDS_ACTION").contains(state)||!Set.of("WAIT","SCAN_QR","OPEN_URL","LOCAL_SETUP","NONE").contains(next))throw invalid();
            if(Set.of("SUCCEEDED","CANCELLED","EXPIRED").contains(state)&&!next.equals("NONE")||state.equals("PENDING")&&next.equals("NONE")||state.equals("NEEDS_ACTION")&&reason==null||state.equals("SUCCEEDED")&&reason!=null)throw invalid();
            if(text(payload,"action").equals("CANCEL")&&!state.equals("CANCELLED"))throw invalid();
            if(value.has("taskId")&&!value.path("taskId").isNull()&&!text(value,"taskId").equals(text(operation,"taskId")))throw invalid();
            Instant deadline=Instant.parse(text(session,"expiresAt")),expiry=value.path("expiresAt").isNull()?deadline:instant(value,"expiresAt");String url=url(value,next,text(session,"platform"));
            if(expiry.isAfter(deadline))expiry=deadline;
            if(state.equals("NEEDS_ACTION")&&driveSetup(session,reason)) {next="LOCAL_SETUP";url=null;}
            if(!text(session,"taskId").equals(text(operation,"taskId"))||!open(session))return true;
            if(expired(session)||!expiry.isAfter(store.clock.instant())) {ended(session,"EXPIRED","AUTH_SESSION_EXPIRED");enqueue(session,"CANCEL");}
            else {
                session.put("state",state);session.put("nextAction",next);session.put("url",url);session.put("reasonCode",reason);session.put("expiresAt",expiry.toString());session.put("message",message(state,next,reason));bump(session);
            }
            store.put("authSession",text(session,"authSessionId"),session);
        }
        return true;
    }
    public void reported(JsonNode operation,JsonNode request,String status) throws SQLException {
        if(!Set.of("CHECK_ACCOUNT","AUTH_STEP").contains(text(operation,"operationType")))return;
        fields(request,Set.of("eventId","runnerId","leaseId","attempt","fencingToken","status","result","error"));
        if(text(request,"status").equals("SUCCEEDED"))return;
        identity(operation);JsonNode error=request.path("error");String code=null;
        if(error.isObject()) {fields(error,Set.of("code","message","stage","retryAfterSeconds","retryAfter","completedEffects","pendingEffects","safeToResume"));code=reason(object("reasonCode",error.path("code")));}
        if(text(operation,"operationType").equals("CHECK_ACCOUNT")) {
            if(!Set.of("PAUSED","CANCELLED").contains(status)) {
                String accountId=text(operation.path("payload"),"accountId");ObjectNode latest=store.optional("accountCheck",accountId);
                if(latest!=null&&text(latest,"taskId").equals(text(operation,"taskId"))) {
                    ObjectNode account=account(accountId);account.put("state","ERROR");account.put("reasonCode",code==null?"ACCOUNT_CHECK_FAILED":code);bump(account);store.put("account",accountId,account);
                }
            }
            return;
        }
        ObjectNode session=store.get("authSession",text(operation.path("payload"),"authSessionId"));
        if(!open(session)||!text(session,"taskId").equals(text(operation,"taskId")))return;
        if(expired(session))ended(session,"EXPIRED","AUTH_SESSION_EXPIRED");
        else if(status.equals("CANCELLED"))ended(session,"CANCELLED",null);
        else if(driveSetup(session,code)) {session.put("state","NEEDS_ACTION");session.put("nextAction","LOCAL_SETUP");session.putNull("url");session.put("reasonCode",code);session.put("message",message("NEEDS_ACTION","LOCAL_SETUP",code));bump(session);}
        else if(!Set.of("WAITING_RETRY","PAUSED").contains(status))ended(session,"NEEDS_ACTION",code==null?"AUTH_SESSION_INTERRUPTED":code);
        else {session.put("reasonCode",code);session.put("message",status.equals("PAUSED")?"授权操作已暂停":"授权操作暂时失败，等待有限重试");bump(session);}
        store.put("authSession",text(session,"authSessionId"),session);
    }
    private void identity(JsonNode operation) {
        JsonNode payload=operation.path("payload");String platform=text(payload,"platform"),account=ACCOUNTS.get(platform);
        if(account==null||!account.equals(text(payload,"accountId"))||!account.equals(text(operation,"accountRef"))||!route(platform).equals(text(operation,"platform"))||!operation.path("destination").isNull()||payload.path("schemaVersion").asInt()!=1)throw invalid();
    }
    private boolean expired(JsonNode session) {return !Instant.parse(text(session,"expiresAt")).isAfter(store.clock.instant());}
    private static boolean open(JsonNode session) {return Set.of("PENDING","NEEDS_ACTION").contains(text(session,"state"));}
    private static void bump(ObjectNode object) {object.put("revision",Long.toString(Long.parseLong(text(object,"revision"))+1));}
    private static void ended(ObjectNode session,String state,String reason) {session.put("state",state);session.put("nextAction","NONE");session.putNull("url");session.put("reasonCode",reason);session.put("message",message(state,"NONE",reason));bump(session);}
    private static String route(String platform) {return platform.equals("GOOGLE_DRIVE")?"BILIBILI":platform;}
    private static String label(String platform) {return switch(platform) {case "BILIBILI"->"Bilibili 主账号";case "TELEGRAM"->"Telegram 主账号";case "QUARK"->"夸克主账号";default->"Google Drive 主账号";};}
    private static boolean driveSetup(JsonNode session,String reason) {return text(session,"platform").equals("GOOGLE_DRIVE")&&reason!=null&&Set.of("AUTH_CLIENT_REQUIRED","AUTH_SESSION_NOT_FOUND","AUTH_MANAGED_EXTERNALLY").contains(reason);}
    private static String message(String state,String next,String reason) {
        if(state.equals("SUCCEEDED"))return "授权流程已完成，请主动检查账号";
        if(state.equals("CANCELLED"))return "授权流程已取消";
        if(state.equals("EXPIRED"))return "授权流程已过期，请重新开始";
        if(next.equals("LOCAL_SETUP")&&"AUTH_MANAGED_EXTERNALLY".equals(reason))return "Google Drive 由指定 rclone 配置管理，请在 Y7000P 完成该 remote 授权后主动检查";
        if(next.equals("LOCAL_SETUP")&&"AUTH_CLIENT_REQUIRED".equals(reason))return "Y7000P 尚未配置新的 Google Desktop OAuth client，请在受控本地入口完成配置后主动检查；Google 官方授权须由用户亲自完成";
        if(next.equals("LOCAL_SETUP")&&"AUTH_SESSION_NOT_FOUND".equals(reason))return "Y7000P 未找到本次授权会话，请在受控本地入口配置新会话后主动检查，或取消本流程后重新开始";
        return switch(next) {case "SCAN_QR"->"请使用官方应用扫描二维码";case "OPEN_URL"->"请在 Y7000P 亲自打开官方授权页完成授权";case "LOCAL_SETUP"->"请在 Y7000P 的受控本地入口配置新会话或 Desktop OAuth client，然后主动检查";case "WAIT"->"等待授权完成，请主动查询进度";default->"授权流程未完成（"+reason+"），请取消后重新开始，或在 Y7000P 检查本地配置";};
    }
    private static void fields(JsonNode node,Set<String> allowed) {
        if(!node.isObject())throw invalid();node.fieldNames().forEachRemaining(key->{if(!allowed.contains(key))throw invalid();});
    }
    private static void factFields(JsonNode node,Set<String> allowed,Set<String> optional) {
        fields(node,allowed);for(String field:allowed)if(!optional.contains(field)&&!node.has(field))throw invalid();
        if(node.has("revision")&&(!node.path("revision").isTextual()||text(node,"revision").isBlank()))throw invalid();
    }
    private static String reason(JsonNode node) {
        if(node.path("reasonCode").isMissingNode()||node.path("reasonCode").isNull())return null;
        if(!node.path("reasonCode").isTextual()||!text(node,"reasonCode").matches("[A-Z][A-Z0-9_]{0,79}"))throw invalid();return text(node,"reasonCode");
    }
    private static Instant instant(JsonNode node,String field) {try {return Instant.parse(required(node,field));}catch(RuntimeException e){throw invalid();}}
    private static String url(JsonNode node,String next,String platform) {
        if(!Set.of("SCAN_QR","OPEN_URL").contains(next)) {if(!node.path("url").isMissingNode()&&!node.path("url").isNull())throw invalid();return null;}
        String value=required(node,"url");if(value.length()>4096||value.chars().anyMatch(c->c<32||c==127))throw invalid();
        try {
            URI uri=URI.create(value);if(!"https".equals(uri.getScheme())||uri.getUserInfo()!=null||uri.getFragment()!=null||uri.getPort()!=-1&&uri.getPort()!=443)throw invalid();
            boolean bilibiliScan=platform.equals("BILIBILI")&&next.equals("SCAN_QR");
            if(bilibiliScan&&!Objects.equals(uri.getRawAuthority(),uri.getHost()))throw invalid();
            boolean currentBilibiliScan=bilibiliScan&&"account.bilibili.com".equals(uri.getHost())&&"/h5/account-h5/auth/scan-web".equals(uri.getPath());
            Set<String> parameters;
            if(currentBilibiliScan)parameters=Set.of("navhide","callback","from","qrcode_key");
            else if(platform.equals("BILIBILI")&&"passport.bilibili.com".equals(uri.getHost())&&Set.of("/h5-app/passport/login/scan","/qrcode/h5/login").contains(uri.getPath()))
                parameters=Set.of("qrcode_key","oauthKey","key","navhide","gourl","source","from","scanning","tab");
            else if(platform.equals("GOOGLE_DRIVE")&&next.equals("OPEN_URL")&&"accounts.google.com".equals(uri.getHost())&&Set.of("/o/oauth2/v2/auth","/o/oauth2/auth").contains(uri.getPath()))
                parameters=Set.of("client_id","redirect_uri","response_type","scope","state","access_type","prompt","include_granted_scopes","code_challenge","code_challenge_method");
            else throw invalid();
            Set<String> seen=new HashSet<>();
            if(uri.getRawQuery()!=null)for(String pair:uri.getRawQuery().split("&")) {
                String[] parts=pair.split("=",2);String key=URLDecoder.decode(parts[0],StandardCharsets.UTF_8),content=parts.length==2?URLDecoder.decode(parts[1],StandardCharsets.UTF_8):"";
                if(!parameters.contains(key)||content.chars().anyMatch(c->c<32||c==127))throw invalid();
                if(bilibiliScan&&!seen.add(key))throw invalid();
                if(currentBilibiliScan&&(key.equals("navhide")&&!content.equals("1")||key.equals("callback")&&!content.equals("close")||key.equals("from")&&!content.isEmpty()||key.equals("qrcode_key")&&content.isEmpty()))throw invalid();
                if(key.equals("redirect_uri")) {URI redirect=URI.create(content);if(!"http".equals(redirect.getScheme())||!Set.of("127.0.0.1","localhost","[::1]").contains(Objects.toString(redirect.getHost(),""))||redirect.getUserInfo()!=null||redirect.getFragment()!=null)throw invalid();}
                if(key.equals("gourl")&&!Set.of("https://www.bilibili.com","https://www.bilibili.com/").contains(content))throw invalid();
                if(key.equals("response_type")&&!content.equals("code"))throw invalid();
            }
            if(currentBilibiliScan&&!seen.equals(parameters))throw invalid();
            return value;
        } catch(RuntimeException e) {throw invalid();}
    }
    private static Failure invalid() {return Failure.bad("INVALID_INPUT","Invalid account or authorization facts");}
}
