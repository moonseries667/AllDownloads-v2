package dev.alldownloads.core;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.json.JsonMapper;
import java.io.IOException;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.channels.OverlappingFileLockException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.sql.*;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.HexFormat;
import java.util.Locale;
import java.util.UUID;
import static dev.alldownloads.core.Model.*;

/** SQLite owner for the isolated M1 fixture. No platform I/O or transport is implemented here. */
public final class TaskStore implements AutoCloseable {
    private final Connection db;
    private final Clock clock;
    private final FileChannel lockChannel;
    private final FileLock lock;
    private final ObjectMapper json = JsonMapper.builder().findAndAddModules().build();
    private boolean closed;

    public TaskStore(Path database, Clock clock) throws SQLException, IOException {
        this.clock = clock;
        Path absolute = database.toAbsolutePath().normalize();
        Files.createDirectories(absolute.getParent());
        absolute = absolute.getParent().toRealPath().resolve(absolute.getFileName());
        if (Files.isSymbolicLink(absolute)) throw new IOException("Database must not be a symbolic link");
        lockChannel = FileChannel.open(absolute.resolveSibling(absolute.getFileName() + ".lock"),
                StandardOpenOption.CREATE, StandardOpenOption.WRITE);
        FileLock acquired;
        try { acquired = lockChannel.tryLock(); }
        catch (OverlappingFileLockException e) { acquired = null; }
        if (acquired == null) { lockChannel.close(); throw new IOException("Control directory is already in use"); }
        lock = acquired;
        Connection opened = null;
        try {
            opened = DriverManager.getConnection("jdbc:sqlite:" + absolute);
            db = opened;
            try (Statement s = db.createStatement()) {
                s.execute("PRAGMA busy_timeout=3000");
                s.execute("PRAGMA foreign_keys=ON");
                s.execute("PRAGMA journal_mode=WAL");
                s.execute("PRAGMA synchronous=FULL");
                try (ResultSet r = s.executeQuery("PRAGMA user_version")) {
                    if (r.next() && r.getInt(1) > 1) throw new SQLException("Unsupported database version");
                }
                s.execute("""
                    CREATE TABLE IF NOT EXISTS tasks (
                      id TEXT PRIMARY KEY, resource_key TEXT NOT NULL UNIQUE,
                      destination TEXT NOT NULL COLLATE NOCASE UNIQUE, request TEXT NOT NULL,
                      status TEXT NOT NULL, desired TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
                      error TEXT, retry_at INTEGER, created INTEGER NOT NULL)
                    """);
                s.execute("""
                    CREATE TABLE IF NOT EXISTS operations (
                      id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), stage TEXT NOT NULL,
                      status TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, fence INTEGER NOT NULL DEFAULT 0,
                      lease_id TEXT, runner TEXT, expires INTEGER, retry_at INTEGER, progress_seq INTEGER NOT NULL DEFAULT -1,
                      UNIQUE(task_id, stage))
                    """);
                s.execute("CREATE TABLE IF NOT EXISTS requests (key TEXT PRIMARY KEY, request TEXT NOT NULL, task_id TEXT NOT NULL REFERENCES tasks(id))");
                s.execute("CREATE TABLE IF NOT EXISTS results (event_id TEXT PRIMARY KEY, body TEXT NOT NULL, ack TEXT NOT NULL)");
                s.execute("CREATE TABLE IF NOT EXISTS facts (event_id TEXT PRIMARY KEY, body TEXT NOT NULL, ack TEXT NOT NULL)");
                s.execute("PRAGMA user_version=1");
            }
        } catch (SQLException | RuntimeException e) {
            if (opened != null) opened.close();
            lock.release(); lockChannel.close(); throw e;
        }
    }

    public synchronized Task create(String key, Request request) throws SQLException {
        requireText(key); validate(request);
        return transaction(() -> {
            String body = encode(request);
            String[] known = row("SELECT request,task_id FROM requests WHERE key=?", key);
            if (known != null) {
                if (!known[0].equals(body)) throw new Conflict("IDEMPOTENCY_CONFLICT");
                return get(known[1]);
            }
            String[] same = row("SELECT id,request FROM tasks WHERE resource_key=? OR destination=? COLLATE NOCASE",
                    request.resourceKey(), request.relativePath());
            String id;
            if (same != null) {
                if (!same[1].equals(body)) throw new Conflict("RESOURCE_OR_DESTINATION_CONFLICT");
                id = same[0];
            } else {
                id = id();
                update("INSERT INTO tasks(id,resource_key,destination,request,status,desired,created) VALUES(?,?,?,?,?,?,?)",
                        id, request.resourceKey(), request.relativePath(), body, "QUEUED", "CONTINUE", now());
                update("INSERT INTO operations(id,task_id,stage,status) VALUES(?,?,?,?)", id(), id, Stage.WRITE_FIXTURE, "QUEUED");
            }
            update("INSERT INTO requests VALUES(?,?,?)", key, body, id);
            return get(id);
        });
    }

    public synchronized Task get(String taskId) throws SQLException {
        ensureOpen();
        String[] r = row("SELECT id,request,status,desired,revision,error,retry_at FROM tasks WHERE id=?", taskId);
        if (r == null) throw new Conflict("NOT_FOUND");
        return new Task(r[0], decode(r[1], Request.class), Status.valueOf(r[2]), Action.valueOf(r[3]),
                Long.parseLong(r[4]), r[5], r[6] == null ? null : Instant.ofEpochMilli(Long.parseLong(r[6])));
    }

    public synchronized Lease acquire(String runnerId, Duration leaseDuration) throws SQLException {
        requireText(runnerId); validDuration(leaseDuration);
        return transaction(() -> {
            expire();
            String[] r = row("""
                SELECT o.id FROM operations o JOIN tasks t ON t.id=o.task_id
                WHERE o.status IN ('QUEUED','WAITING_RETRY') AND (o.retry_at IS NULL OR o.retry_at<=?)
                  AND t.desired='CONTINUE' AND t.status IN ('QUEUED','WAITING_RETRY')
                ORDER BY t.created,t.id,o.id LIMIT 1
                """, now());
            if (r == null) return null;
            String lease = id();
            update("UPDATE operations SET status='RUNNING',attempt=attempt+1,fence=fence+1,lease_id=?,runner=?,expires=?,retry_at=NULL,progress_seq=-1 WHERE id=?",
                    lease, runnerId, now() + leaseDuration.toMillis(), r[0]);
            Lease result = lease(r[0]);
            update("UPDATE tasks SET status='RUNNING',error=NULL,retry_at=NULL,revision=revision+1 WHERE id=?", result.taskId());
            return result;
        });
    }

    public synchronized Heartbeat heartbeat(Lease proof, String eventId, long sequence, Duration extension) throws SQLException {
        requireText(eventId); validDuration(extension);
        if (sequence < 0) throw new IllegalArgumentException("Negative progress sequence");
        return transaction(() -> {
            String body = encode(proof) + ":" + sequence + ":" + extension;
            String[] prior = row("SELECT body,ack FROM facts WHERE event_id=?", eventId);
            if (prior != null) {
                if (!prior[0].equals(body)) throw new Conflict("EVENT_CONFLICT");
                return decode(prior[1], Heartbeat.class);
            }
            Lease current = checkLease(proof, false);
            // A delayed progress event cannot extend a lease or overwrite newer progress.
            update("UPDATE operations SET progress_seq=?,expires=? WHERE id=? AND progress_seq<?",
                    sequence, Math.max(current.expiresAt().toEpochMilli(), now() + extension.toMillis()), proof.operationId(), sequence);
            Heartbeat ack = new Heartbeat(get(proof.taskId()).desiredAction(), lease(proof.operationId()).expiresAt());
            update("INSERT INTO facts VALUES(?,?,?)", eventId, body, encode(ack));
            return ack;
        });
    }

    public synchronized Task control(String taskId, long revision, Action action) throws SQLException {
        return transaction(() -> {
            Task t = get(taskId);
            if (t.revision() != revision) throw new Conflict("VERSION_CHANGED");
            if (t.status() == Status.SUCCEEDED || t.status() == Status.CANCELLED) throw new Conflict("ACTION_NOT_ALLOWED");
            String[] op = row("SELECT status FROM operations WHERE task_id=? AND status<>'SUCCEEDED'", taskId);
            boolean active = op != null && (op[0].equals("RUNNING") || op[0].equals("NEEDS_ACTION"));
            switch (action) {
                case PAUSE, CANCEL -> {
                    if (t.status() == Status.NEEDS_ACTION) throw new Conflict("RECONCILE_REQUIRED");
                    String state = active ? (action == Action.PAUSE ? "PAUSING" : t.status().name())
                            : action == Action.PAUSE ? "PAUSED" : "CANCELLED";
                    update("UPDATE tasks SET status=?,desired=?,revision=revision+1 WHERE id=?", state, action, taskId);
                    if (!active) update("UPDATE operations SET status=? WHERE task_id=? AND status<>'SUCCEEDED'", state, taskId);
                }
                case RESUME, RETRY -> {
                    if (action == Action.RESUME && t.status() != Status.PAUSED || action == Action.RETRY && t.status() != Status.FAILED)
                        throw new Conflict("ACTION_NOT_ALLOWED");
                    update("UPDATE tasks SET status='QUEUED',desired='CONTINUE',error=NULL,retry_at=NULL,revision=revision+1 WHERE id=?", taskId);
                    update("UPDATE operations SET status='QUEUED',retry_at=NULL WHERE task_id=? AND status<>'SUCCEEDED'", taskId);
                }
                default -> throw new Conflict("ACTION_NOT_ALLOWED");
            }
            return get(taskId);
        });
    }

    public synchronized void recoverExpired() throws SQLException { transaction(() -> { expire(); return null; }); }

    /** Normal delivery requires a live lease; reconciliation accepts only the retained old lease. */
    public synchronized Ack complete(Result result) throws SQLException { return accept(result, false); }
    public synchronized Ack reconcile(Result result) throws SQLException { return accept(result, true); }

    private Ack accept(Result result, boolean reconciliation) throws SQLException {
        if (result == null || result.lease() == null || result.outcome() == null) throw new IllegalArgumentException("Result required");
        requireText(result.eventId());
        if (result.outcome() != Outcome.SUCCEEDED && result.outcome() != Outcome.PAUSED && result.outcome() != Outcome.CANCELLED) requireText(result.errorCode());
        return transaction(() -> {
            String body = encode(result);
            String[] previous = row("SELECT body,ack FROM results WHERE event_id=?", result.eventId());
            if (previous != null) {
                if (!body.equals(previous[0])) throw new Conflict("EVENT_CONFLICT");
                return decode(previous[1], Ack.class);
            }
            Lease active = checkLease(result.lease(), reconciliation);
            Task task = get(active.taskId());
            String state;
            Long retryAt = null;
            switch (result.outcome()) {
                case SUCCEEDED -> {
                    if (!expectedEvidence(task.request()).equals(result.evidence())) throw new Conflict("VERIFICATION_FAILED");
                    update("UPDATE operations SET status='SUCCEEDED' WHERE id=?", active.operationId());
                    if (active.stage() == Stage.WRITE_FIXTURE) {
                        // Result, next stage and acknowledgement are committed atomically.
                        state = task.desiredAction() == Action.CANCEL ? "CANCELLED" : task.desiredAction() == Action.PAUSE ? "PAUSED" : "QUEUED";
                        update("INSERT INTO operations(id,task_id,stage,status) VALUES(?,?,?,?)", id(), task.id(), Stage.VERIFY_FIXTURE, state);
                    } else state = "SUCCEEDED";
                }
                case CANCELLED, PAUSED -> {
                    if (result.evidence() != null) throw new Conflict("EFFECTS_REQUIRE_RECONCILIATION");
                    state = result.outcome().name();
                    if (state.equals("PAUSED") && task.desiredAction() == Action.CANCEL) state = "CANCELLED";
                    update("UPDATE operations SET status=? WHERE id=?", state, active.operationId());
                }
                case RETRYABLE_ERROR -> {
                    if (result.evidence() != null) throw new Conflict("EFFECTS_REQUIRE_RECONCILIATION");
                    if (task.desiredAction() == Action.CANCEL) state = "CANCELLED";
                    else if (task.desiredAction() == Action.PAUSE) state = "PAUSED";
                    else if (active.attempt() > 3) state = "FAILED";
                    else {
                        state = "WAITING_RETRY";
                        long delay = new long[] {30_000,120_000,600_000}[active.attempt()-1];
                        if (result.retryAfter() != null) {
                            if (result.retryAfter().isNegative()) throw new IllegalArgumentException("Negative retry delay");
                            delay = Math.max(delay, result.retryAfter().toMillis());
                        }
                        retryAt = Math.addExact(now(), delay);
                    }
                    update("UPDATE operations SET status=?,retry_at=? WHERE id=?", state, retryAt, active.operationId());
                }
                default -> {
                    state = "NEEDS_ACTION";
                    update("UPDATE operations SET status=? WHERE id=?", state, active.operationId());
                }
            }
            Action desired = state.equals("PAUSED") ? Action.PAUSE : state.equals("CANCELLED") ? Action.CANCEL : task.desiredAction();
            update("UPDATE tasks SET status=?,desired=?,error=?,retry_at=?,revision=revision+1 WHERE id=?",
                    state, desired, result.errorCode(), retryAt, task.id());
            Ack ack = new Ack(id(), result.eventId(), task.id(), Status.valueOf(state));
            update("INSERT INTO results VALUES(?,?,?)", result.eventId(), body, encode(ack));
            return ack;
        });
    }

    private Lease checkLease(Lease proof, boolean reconciliation) throws SQLException {
        if (proof == null) throw new IllegalArgumentException("Lease required");
        Lease current = lease(proof.operationId());
        String[] state = row("SELECT status FROM operations WHERE id=?", proof.operationId());
        if (!current.taskId().equals(proof.taskId()) || !current.leaseId().equals(proof.leaseId())
                || !current.runnerId().equals(proof.runnerId()) || current.attempt() != proof.attempt()
                || current.fencingToken() != proof.fencingToken()) throw new Conflict("STALE_LEASE");
        boolean live = state[0].equals("RUNNING") && current.expiresAt().toEpochMilli() > now();
        boolean retained = state[0].equals("NEEDS_ACTION") || state[0].equals("RUNNING");
        if (!live && !(reconciliation && retained)) throw new Conflict("RECONCILE_REQUIRED");
        return current;
    }

    private Lease lease(String operationId) throws SQLException {
        String[] r = row("SELECT id,task_id,stage,attempt,fence,lease_id,runner,expires FROM operations WHERE id=?", operationId);
        if (r == null || r[5] == null) throw new Conflict("LEASE_NOT_FOUND");
        return new Lease(r[0],r[1],Stage.valueOf(r[2]),Integer.parseInt(r[3]),Long.parseLong(r[4]),r[5],r[6],
                Instant.ofEpochMilli(Long.parseLong(r[7])),get(r[1]).request());
    }

    private void expire() throws SQLException {
        update("UPDATE tasks SET status='NEEDS_ACTION',error='RESULT_UNKNOWN',revision=revision+1 WHERE id IN (SELECT task_id FROM operations WHERE status='RUNNING' AND expires<=?)", now());
        update("UPDATE operations SET status='NEEDS_ACTION' WHERE status='RUNNING' AND expires<=?", now());
    }

    public static Evidence expectedEvidence(Request r) {
        byte[] bytes = r.content().getBytes(StandardCharsets.UTF_8);
        try { return new Evidence(r.relativePath(), Long.toString(bytes.length), HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes))); }
        catch (NoSuchAlgorithmException impossible) { throw new IllegalStateException(impossible); }
    }

    private static void validate(Request r) {
        if (r == null || r.content() == null) throw new IllegalArgumentException("Request required");
        requireText(r.resourceKey()); requireText(r.relativePath()); requireText(r.storageRevision());
        if (r.content().getBytes(StandardCharsets.UTF_8).length > 1_048_576) throw new IllegalArgumentException("Fixture exceeds 1 MiB");
        String path = r.relativePath();
        if (path.startsWith("/") || path.contains("\\") || path.contains(":") || path.length() > 240)
            throw new IllegalArgumentException("Invalid relative path");
        for (String part : path.split("/", -1)) {
            String base = part.split("\\.", 2)[0].toUpperCase(Locale.ROOT);
            if (part.isBlank() || part.equals(".") || part.equals("..") || part.endsWith(".") || part.endsWith(" ")
                    || part.matches(".*[<>\"|?*\\p{Cntrl}].*") || base.matches("CON|PRN|AUX|NUL|CONIN\\$|CONOUT\\$|COM[1-9¹²³]|LPT[1-9¹²³]") || part.equalsIgnoreCase(".work"))
                throw new IllegalArgumentException("Invalid path component");
        }
    }
    private static void requireText(String value) { if (value == null || value.isBlank()) throw new IllegalArgumentException("Value required"); }
    private static void validDuration(Duration duration) { if (duration == null || duration.toMillis() < 1 || duration.compareTo(Duration.ofMinutes(5)) > 0) throw new IllegalArgumentException("Lease must be 1 ms to 5 minutes"); }
    private long now() { return clock.millis(); }
    private static String id() { return UUID.randomUUID().toString(); }
    private String encode(Object value) { try { return json.writeValueAsString(value); } catch (IOException e) { throw new IllegalArgumentException(e); } }
    private <T> T decode(String value, Class<T> type) { try { return json.readValue(value, type); } catch (IOException e) { throw new IllegalStateException("Invalid persisted data",e); } }
    private void ensureOpen() throws SQLException { if (closed) throw new SQLException("Store is closed"); }
    private int update(String sql, Object... parameters) throws SQLException {
        try (PreparedStatement s = db.prepareStatement(sql)) { bind(s,parameters); return s.executeUpdate(); }
    }
    private String[] row(String sql, Object... parameters) throws SQLException {
        try (PreparedStatement s = db.prepareStatement(sql)) {
            bind(s,parameters);
            try (ResultSet r = s.executeQuery()) {
                if (!r.next()) return null;
                String[] values = new String[r.getMetaData().getColumnCount()];
                for (int i=0;i<values.length;i++) values[i]=r.getString(i+1);
                return values;
            }
        }
    }
    private static void bind(PreparedStatement s, Object[] parameters) throws SQLException {
        for (int i=0;i<parameters.length;i++) s.setObject(i+1,parameters[i] instanceof Enum<?> e ? e.name() : parameters[i]);
    }
    @FunctionalInterface private interface Transaction<T> { T run() throws SQLException; }
    private <T> T transaction(Transaction<T> work) throws SQLException {
        ensureOpen(); db.setAutoCommit(false);
        try { T result=work.run(); db.commit(); return result; }
        catch (SQLException | RuntimeException e) { try { db.rollback(); } catch (SQLException rollback) { e.addSuppressed(rollback); } throw e; }
        finally { db.setAutoCommit(true); }
    }
    @Override public synchronized void close() throws SQLException, IOException {
        if (closed) return;
        closed=true;
        try { db.close(); } finally { try { lock.release(); } finally { lockChannel.close(); } }
    }
}
