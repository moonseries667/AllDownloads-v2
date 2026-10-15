package dev.alldownloads.feedback2;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.sql.*;
import java.time.Clock;
import java.util.Map;

/** Persistent projection of real errors, shared by every platform. Reading/acknowledging has no task effect. */
public final class IssueRepository extends DomainSql {
    private static final Map<String,String> MESSAGES=Map.ofEntries(
        Map.entry("NETWORK_TEMPORARY","网络暂时不可用，请稍后重试。"),
        Map.entry("AUTH_REQUIRED","账号需要登录，请检查账号状态。"),
        Map.entry("PERMISSION_DENIED","没有访问权限，请检查账号或目标目录。"),
        Map.entry("TARGET_UNAVAILABLE","目标文件夹已失效或不可写，请重新选择。"),
        Map.entry("VERSION_CHANGED","内容或目录版本已改变，请刷新后重试。"),
        Map.entry("INCOMPLETE_MEDIA_GROUP","媒体组尚未完整收录，请再次同步核验。"),
        Map.entry("SNAPSHOT_INCOMPLETE","目录信息尚未完整收录，请刷新后重试。"),
        Map.entry("VERIFICATION_FAILED","成果核验未通过，仍有文件需要补齐。"),
        Map.entry("RESULT_UNKNOWN","执行结果尚未确认，请核验后继续。"),
        Map.entry("PROVIDER_CHANGED","平台返回的信息发生变化，需要重新核验。"),
        Map.entry("RATE_LIMITED","平台暂时限制请求，请等待后重试。"),
        Map.entry("INSUFFICIENT_SPACE","本机可用空间不足，请处理后重试。"));
    public IssueRepository(Connection db,ObjectMapper json,Clock clock) { super(db,json,clock); }
    public void initialize() throws SQLException { writing(); }
    public ObjectNode record(String taskId,String objectId,String code,String severity) throws SQLException {
        writing(); require(taskId!=null || objectId!=null,"INVALID_INPUT"); require(code!=null && code.matches("[A-Z][A-Z0-9_]{0,79}"),"INVALID_INPUT");
        require("ERROR".equals(severity)||"WARNING".equals(severity),"INVALID_INPUT");
        String id=dev.alldownloads.business.PathPlanner.digest((taskId==null?"object:"+objectId:taskId)+":"+code);
        var prior=row("SELECT body FROM objects WHERE kind='issue' AND id=?",id);ObjectNode old=prior==null?null:read(prior[0]);
        ObjectNode value=obj().put("id",id).put("taskId",taskId).put("objectId",objectId).put("code",code)
            .put("message",MESSAGES.getOrDefault(code,dev.alldownloads.business.OperationQueue.message(code))).put("severity",severity)
            .put("occurredAt",old==null?now():old.path("occurredAt").asText()).put("updatedAt",now())
            .put("read",old!=null&&!old.path("resolved").asBoolean()&&old.path("read").asBoolean()).put("resolved",false);
        save(value);return value;
    }
    public ObjectNode get(String id) throws SQLException {
        var r=row("SELECT body FROM objects WHERE kind='issue' AND id=?",id);require(r!=null,"NOT_FOUND");return read(r[0]);
    }
    public ArrayNode list(boolean includeResolved) throws SQLException {
        ArrayNode a=array(); for(var r:rows("SELECT body FROM objects WHERE kind='issue'"+(includeResolved?"":" AND json_extract(body,'$.resolved')=0")+" ORDER BY json_extract(body,'$.occurredAt') DESC,id"))a.add(read(r[0]));return a;
    }
    public ObjectNode acknowledge(String id) throws SQLException {writing();ObjectNode value=get(id).put("read",true);save(value);return value;}
    public void taskSucceeded(String taskId) throws SQLException {writing();for(var n:list(false))if(taskId.equals(nullable(n,"taskId"))&&n.path("severity").asText().equals("ERROR"))save(((ObjectNode)n).put("resolved",true).put("updatedAt",now()));}
    public void resolveObject(String objectId,String code) throws SQLException {writing();for(var n:list(false))if(objectId.equals(nullable(n,"objectId"))&&code.equals(n.path("code").asText()))save(((ObjectNode)n).put("resolved",true).put("updatedAt",now()));}
    private void save(ObjectNode value) throws SQLException {exec("INSERT INTO objects(kind,id,body) VALUES('issue',?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body",value.path("id").asText(),value.toString());}
}
