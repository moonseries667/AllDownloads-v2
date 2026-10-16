package dev.alldownloads.feedback2;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.charset.StandardCharsets;
import java.sql.*;
import java.time.Clock;
import java.util.*;

/** Participates in the control store's connection and transaction; owns no queue or database. */
public class DomainSql {
    protected final Connection db;
    protected final ObjectMapper json;
    protected final Clock clock;
    public DomainSql(Connection db, ObjectMapper json, Clock clock) { this.db=db; this.json=json; this.clock=clock; }
    public static final class DomainError extends RuntimeException {
        public final String code;
        public DomainError(String code) { super(code); this.code=code; }
    }
    @FunctionalInterface public interface Enqueue {
        String add(String type, String platform, String accountId, String resourceKey, ObjectNode payload) throws SQLException;
    }
    protected void writing() throws SQLException {
        if (db.getAutoCommit()) throw new IllegalStateException("Domain writes require the control-store transaction");
    }
    protected void ddl(String... statements) throws SQLException {
        writing(); try (Statement s=db.createStatement()) { for(String sql:statements) s.execute(sql); }
    }
    protected int exec(String sql,Object... args) throws SQLException {
        try(PreparedStatement s=db.prepareStatement(sql)) { bind(s,args); return s.executeUpdate(); }
    }
    protected List<String[]> rows(String sql,Object... args) throws SQLException {
        try(PreparedStatement s=db.prepareStatement(sql)) {
            bind(s,args); try(ResultSet r=s.executeQuery()) {
                List<String[]> out=new ArrayList<>(); int n=r.getMetaData().getColumnCount();
                while(r.next()) { String[] a=new String[n]; for(int i=0;i<n;i++) a[i]=r.getString(i+1); out.add(a); } return out;
            }
        }
    }
    protected String[] row(String sql,Object... args) throws SQLException { var a=rows(sql,args); return a.isEmpty()?null:a.getFirst(); }
    private static void bind(PreparedStatement s,Object[] args) throws SQLException { for(int i=0;i<args.length;i++) s.setObject(i+1,args[i]); }
    protected ObjectNode obj() { return json.createObjectNode(); }
    protected ArrayNode array() { return json.createArrayNode(); }
    protected ObjectNode read(String s) { try { return (ObjectNode)json.readTree(s); } catch(Exception e) { throw new IllegalStateException("Invalid stored domain record",e); } }
    protected String now() { return clock.instant().toString(); }
    protected static String text(JsonNode n,String key) { var v=n.get(key); if(v==null || !v.isTextual() || v.asText().isBlank()) throw new DomainError("INVALID_INPUT"); return v.asText(); }
    protected static String nullable(JsonNode n,String key) { var v=n.get(key); return v==null || v.isNull()?null:text(n,key); }
    protected static String stable(String prefix,String... parts) { return prefix+UUID.nameUUIDFromBytes(String.join("\u0000",parts).getBytes(StandardCharsets.UTF_8)); }
    protected static void require(boolean condition,String code) { if(!condition) throw new DomainError(code); }
    protected static String segment(String s) {
        String value=s.replaceAll("[<>:\"/\\\\|?*\\p{Cntrl}]","_").replaceAll("[. ]+$","");
        if(value.isBlank()) value="attachment";
        if(value.matches("(?i)(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\\..*)?")) value="_"+value;
        return value.length()>100?value.substring(0,100):value;
    }
}
