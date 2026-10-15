package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.io.IOException;
import java.nio.channels.*;
import java.nio.file.*;
import java.sql.*;
import java.time.Clock;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** The production business database owner. The older TaskStore remains an isolated fixture only. */
public final class BusinessStore implements AutoCloseable {
    private final Connection db;
    private final FileChannel channel;
    private final FileLock lock;
    public final Clock clock;
    public BusinessStore(Path path,Clock clock) throws SQLException,IOException {
        this.clock=clock;
        path=path.toAbsolutePath().normalize(); Files.createDirectories(path.getParent());
        if(Files.isSymbolicLink(path)) throw new IOException("Database must not be a symbolic link");
        channel=FileChannel.open(path.resolveSibling(path.getFileName()+".lock"),StandardOpenOption.CREATE,StandardOpenOption.WRITE);
        FileLock acquired;
        try { acquired=channel.tryLock(); } catch(OverlappingFileLockException e) { acquired=null; }
        if(acquired==null) { channel.close();throw new IOException("Business database already in use"); }
        lock=acquired;
        Connection opened=null;
        try {
            opened=DriverManager.getConnection("jdbc:sqlite:"+path);db=opened;
            try(Statement s=db.createStatement()) {
                s.execute("PRAGMA busy_timeout=3000");s.execute("PRAGMA foreign_keys=ON");s.execute("PRAGMA journal_mode=WAL");s.execute("PRAGMA synchronous=FULL");
                try(ResultSet r=s.executeQuery("PRAGMA user_version")) { if(r.next() && r.getInt(1)>1) throw new SQLException("Unsupported business database version"); }
                s.execute("CREATE TABLE IF NOT EXISTS objects(kind TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(kind,id))");
                s.execute("CREATE TABLE IF NOT EXISTS requests(scope TEXT NOT NULL,key TEXT NOT NULL,body TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key))");
                s.execute("CREATE TABLE IF NOT EXISTS operations(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,type TEXT NOT NULL,resource_key TEXT NOT NULL,body TEXT NOT NULL,status TEXT NOT NULL,attempt INTEGER NOT NULL DEFAULT 0,fence INTEGER NOT NULL DEFAULT 0,lease_id TEXT,runner TEXT,expires INTEGER,retry_at INTEGER,seq INTEGER NOT NULL DEFAULT -1,created INTEGER NOT NULL)");
                s.execute("CREATE INDEX IF NOT EXISTS operation_queue ON operations(status,type,retry_at,created)");
                s.execute("CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY,body TEXT NOT NULL,ack TEXT NOT NULL)");
                s.execute("CREATE TABLE IF NOT EXISTS paths(identity TEXT PRIMARY KEY,path TEXT NOT NULL COLLATE NOCASE UNIQUE)");
                s.execute("CREATE TABLE IF NOT EXISTS output_claims(root_id TEXT NOT NULL,output_id TEXT NOT NULL,task_id TEXT NOT NULL,PRIMARY KEY(root_id,output_id))");
                s.execute("PRAGMA user_version=1");
            }
        } catch(SQLException|RuntimeException e) { if(opened!=null) opened.close();lock.release();channel.close();throw e; }
    }
    @FunctionalInterface public interface Work<T> { T run() throws Exception; }
    public synchronized <T> T tx(Work<T> work) {
        try {
            db.setAutoCommit(false);
            try { T value=work.run(); db.commit(); return value; }
            catch(Exception e) { db.rollback();if(e instanceof RuntimeException r) throw r;throw new IllegalStateException("Business transaction failed",e); }
            finally { db.setAutoCommit(true); }
        } catch(SQLException e) { throw new IllegalStateException("Business database unavailable",e); }
    }
    public ObjectNode get(String kind,String id) throws SQLException { String[] r=row("SELECT body FROM objects WHERE kind=? AND id=?",kind,id);if(r==null) throw Failure.missing();return (ObjectNode)parse(r[0]); }
    public ObjectNode optional(String kind,String id) throws SQLException { String[] r=row("SELECT body FROM objects WHERE kind=? AND id=?",kind,id);return r==null?null:(ObjectNode)parse(r[0]); }
    public List<JsonNode> all(String kind) throws SQLException { List<JsonNode> out=new ArrayList<>();for(String[] r:rows("SELECT body FROM objects WHERE kind=? ORDER BY id",kind)) out.add(parse(r[0]));return out; }
    public void put(String kind,String id,JsonNode body) throws SQLException { update("INSERT INTO objects VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body",kind,id,body.toString()); }
    public ObjectNode once(String scope,String key,JsonNode request,Work<ObjectNode> work) throws Exception {
        if(key==null||key.isBlank()||key.length()>200) throw Failure.bad("INVALID_INPUT","Idempotency-Key required");
        String body=canonical(request);String[] old=row("SELECT body,response FROM requests WHERE scope=? AND key=?",scope,key);
        if(old!=null) { if(!old[0].equals(body)) throw Failure.conflict("IDEMPOTENCY_CONFLICT");return (ObjectNode)parse(old[1]); }
        ObjectNode response=work.run();update("INSERT INTO requests VALUES(?,?,?,?)",scope,key,body,response.toString());return response;
    }
    public int update(String sql,Object... args) throws SQLException { try(PreparedStatement s=db.prepareStatement(sql)) { bind(s,args);return s.executeUpdate(); } }
    public List<String[]> rows(String sql,Object... args) throws SQLException {
        try(PreparedStatement s=db.prepareStatement(sql)) { bind(s,args);try(ResultSet r=s.executeQuery()) { List<String[]> out=new ArrayList<>();while(r.next()) { String[] v=new String[r.getMetaData().getColumnCount()];for(int i=0;i<v.length;i++)v[i]=r.getString(i+1);out.add(v); }return out; } }
    }
    public String[] row(String sql,Object... args) throws SQLException { List<String[]> rows=rows(sql,args);return rows.isEmpty()?null:rows.getFirst(); }
    private static void bind(PreparedStatement s,Object[] args) throws SQLException { for(int i=0;i<args.length;i++)s.setObject(i+1,args[i]); }
    public String now() { return clock.instant().toString(); }
    /** Domain extensions participate in this store's transaction and must never close the connection. */
    public Connection domainConnection() { return db; }
    public long millis() { return clock.millis(); }
    @Override public synchronized void close() throws SQLException,IOException { try { db.close(); } finally { try { lock.release(); } finally { channel.close(); } } }
}
