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
    public void initialize() throws SQLException { ddl("""
        CREATE TABLE IF NOT EXISTS f2_issues(id TEXT PRIMARY KEY,task_id TEXT,object_id TEXT,
        code TEXT NOT NULL,severity TEXT NOT NULL,occurred TEXT NOT NULL,updated TEXT NOT NULL,
        is_read INTEGER NOT NULL DEFAULT 0,resolved INTEGER NOT NULL DEFAULT 0)
        """); }
    public ObjectNode record(String taskId,String objectId,String code,String severity) throws SQLException {
        writing(); require(taskId!=null || objectId!=null,"INVALID_INPUT"); require(code!=null && code.matches("[A-Z][A-Z0-9_]{0,79}"),"INVALID_INPUT");
        require("ERROR".equals(severity)||"WARNING".equals(severity),"INVALID_INPUT");
        String id=stable("issue-",taskId==null?"object:"+objectId:"task:"+taskId,code);
        exec("""
            INSERT INTO f2_issues(id,task_id,object_id,code,severity,occurred,updated) VALUES(?,?,?,?,?,?,?)
            ON CONFLICT(id) DO UPDATE SET updated=excluded.updated,severity=excluded.severity,
            is_read=CASE WHEN f2_issues.resolved=1 THEN 0 ELSE f2_issues.is_read END,resolved=0
            """,id,taskId,objectId,code,severity,now(),now()); return get(id);
    }
    public ObjectNode get(String id) throws SQLException {
        var r=row("SELECT id,task_id,object_id,code,severity,occurred,updated,is_read,resolved FROM f2_issues WHERE id=?",id);
        require(r!=null,"NOT_FOUND"); return view(r);
    }
    public ArrayNode list(boolean includeResolved) throws SQLException {
        ArrayNode a=array(); for(var r:rows("SELECT id,task_id,object_id,code,severity,occurred,updated,is_read,resolved FROM f2_issues"+(includeResolved?"":" WHERE resolved=0")+" ORDER BY occurred DESC,id")) a.add(view(r)); return a;
    }
    public ObjectNode acknowledge(String id) throws SQLException { writing(); require(exec("UPDATE f2_issues SET is_read=1 WHERE id=?",id)==1,"NOT_FOUND"); return get(id); }
    public void taskSucceeded(String taskId) throws SQLException { writing(); exec("UPDATE f2_issues SET resolved=1,updated=? WHERE task_id=? AND severity='ERROR' AND resolved=0",now(),taskId); }
    public void resolveObject(String objectId,String code) throws SQLException { writing(); exec("UPDATE f2_issues SET resolved=1,updated=? WHERE object_id=? AND code=? AND resolved=0",now(),objectId,code); }
    private ObjectNode view(String[] r) {
        return obj().put("id",r[0]).put("taskId",r[1]).put("objectId",r[2]).put("code",r[3])
            .put("message",MESSAGES.getOrDefault(r[3],"操作未完成，请查看关联任务并处理。"))
            .put("severity",r[4]).put("occurredAt",r[5]).put("updatedAt",r[6]).put("read","1".equals(r[7])).put("resolved","1".equals(r[8]));
    }
}
