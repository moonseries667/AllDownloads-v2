package dev.alldownloads.core;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Path;
import java.sql.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;

import static dev.alldownloads.core.Model.*;
import static org.junit.jupiter.api.Assertions.*;

class TaskStoreTest {
    private static final Instant START = Instant.parse("2026-10-15T00:00:00Z");
    private static final Duration LEASE = Duration.ofSeconds(30);
    @TempDir Path temp;
    private Path database;
    private MutableClock clock;

    @BeforeEach void setUp() {
        database = temp.resolve("control.sqlite");
        clock = new MutableClock(START);
    }

    @Test void idempotencyValidationRestartAndSecondInstanceLock() throws Exception {
        Request request = request();
        Task first;
        try (TaskStore store = open()) {
            first = store.create("key-1", request);
            assertEquals(first.id(), store.create("key-1", request).id());
            assertEquals(first.id(), store.create("key-2", request).id());
            assertEquals("IDEMPOTENCY_CONFLICT", assertThrows(Conflict.class,
                    () -> store.create("key-1", changed(request))).getMessage());
            assertEquals("RESOURCE_OR_DESTINATION_CONFLICT", assertThrows(Conflict.class,
                    () -> store.create("key-3", changed(request))).getMessage());
            assertTrue(assertThrows(IOException.class, () -> new TaskStore(database, clock))
                    .getMessage().contains("already in use"));
            List<String> paths = List.of("../x", "dir/../x", "/x", "C:/x", "dir\\x",
                    "CON.txt", ".work/x", "dir/end.", "dir/end ");
            for (int i = 0; i < paths.size(); i++) {
                int n = i;
                Request invalid = new Request("bad-" + n, paths.get(i), "body", "storage-r1");
                assertThrows(IllegalArgumentException.class, () -> store.create("bad-key-" + n, invalid));
            }
            assertEquals(1, scalar("SELECT COUNT(*) FROM tasks"));
            assertEquals(2, scalar("SELECT COUNT(*) FROM requests"));
        }
        try (TaskStore reopened = open()) {
            assertEquals(first, reopened.get(first.id()));
            assertEquals("storage-r17", reopened.get(first.id()).request().storageRevision());
        }
    }

    @Test void evidenceAndInjectedInsertFailureRollBackThenReplayOneAckAndVerify() throws Exception {
        Request request = request();
        try (TaskStore store = open()) {
            Task task = store.create("atomic-key", request);
            Lease write = store.acquire("runner-a", LEASE);
            Evidence evidence = TaskStore.expectedEvidence(request);
            Evidence falseEvidence = new Evidence(evidence.relativePath(), "9999", evidence.sha256());
            assertEquals("VERIFICATION_FAILED", assertThrows(Conflict.class, () -> store.complete(
                    result("write-event", write, Outcome.SUCCEEDED, falseEvidence, null, null))).getMessage());

            try (Connection c = connection(); Statement s = c.createStatement()) {
                s.execute("CREATE TRIGGER reject_result BEFORE INSERT ON results "
                        + "BEGIN SELECT RAISE(ABORT, 'injected'); END");
            }
            Result success = result("write-event", write, Outcome.SUCCEEDED, evidence, null, null);
            assertThrows(SQLException.class, () -> store.complete(success));
            assertEquals(Status.RUNNING, store.get(task.id()).status());
            assertEquals("RUNNING", operationStatus(task.id(), Stage.WRITE_FIXTURE));
            assertEquals(0, scalar("SELECT COUNT(*) FROM operations WHERE stage='VERIFY_FIXTURE'"));
            assertEquals(0, scalar("SELECT COUNT(*) FROM results"));
            try (Connection c = connection(); Statement s = c.createStatement()) { s.execute("DROP TRIGGER reject_result"); }

            Ack ack = store.complete(success);
            assertEquals(ack, store.complete(success));
            assertEquals(Status.QUEUED, ack.status());
            assertEquals(1, scalar("SELECT COUNT(*) FROM operations WHERE stage='VERIFY_FIXTURE'"));
            assertEquals("EVENT_CONFLICT", assertThrows(Conflict.class, () -> store.complete(
                    result("write-event", write, Outcome.SUCCEEDED, evidence, "changed", null))).getMessage());

            Lease verify = store.acquire("runner-a", LEASE);
            assertEquals(Stage.VERIFY_FIXTURE, verify.stage());
            store.complete(result("verify-event", verify, Outcome.SUCCEEDED, evidence, null, null));
            Task done = store.get(task.id());
            assertEquals(Status.SUCCEEDED, done.status());
            assertEquals("ACTION_NOT_ALLOWED", assertThrows(Conflict.class,
                    () -> store.control(task.id(), done.revision(), Action.CANCEL)).getMessage());
            assertNull(store.acquire("runner-a", LEASE));
        }
    }

    @Test void expiredLeaseReconcilesSafeRetryWithBackoffAndFencesOldProofs() throws Exception {
        try (TaskStore store = open()) {
            Task task = store.create("retry-reconcile", request());
            Lease old = store.acquire("runner-a", Duration.ofSeconds(10));
            clock.advance(Duration.ofSeconds(11));
            assertNull(store.acquire("runner-b", LEASE));
            assertEquals(Status.NEEDS_ACTION, store.get(task.id()).status());
            assertEquals("RECONCILE_REQUIRED", assertThrows(Conflict.class, () -> store.complete(
                    result("late", old, Outcome.RETRYABLE_ERROR, null, "TRANSIENT", null))).getMessage());
            assertEquals("STALE_LEASE", assertThrows(Conflict.class, () -> store.reconcile(
                    result("forged", withRunner(old, "runner-b"), Outcome.RETRYABLE_ERROR, null, "TRANSIENT", null)))
                    .getMessage());

            assertEquals(Status.WAITING_RETRY, store.reconcile(result("safe-error", old,
                    Outcome.RETRYABLE_ERROR, null, "TRANSIENT", Duration.ofSeconds(60))).status());
            assertEquals(clock.instant().plusSeconds(60), store.get(task.id()).nextRetryAt());
            assertNull(store.acquire("runner-b", LEASE));
            clock.advance(Duration.ofSeconds(60));
            Lease next = store.acquire("runner-b", LEASE);
            assertEquals(2, next.attempt());
            assertEquals(old.fencingToken() + 1, next.fencingToken());
            assertEquals("STALE_LEASE", assertThrows(Conflict.class, () -> store.reconcile(
                    result("old-after-retry", old, Outcome.RETRYABLE_ERROR, null, "TRANSIENT", null))).getMessage());
            assertEquals("STALE_LEASE", assertThrows(Conflict.class, () -> store.complete(
                    result("wrong-runner", withRunner(next, "other"), Outcome.RETRYABLE_ERROR,
                            null, "TRANSIENT", null))).getMessage());
        }
    }

    @Test void unknownWaitsAcrossRestartAndReconciledSuccessGoesStraightToVerify() throws Exception {
        Task unknown;
        try (TaskStore store = open()) {
            unknown = store.create("unknown-key", request());
            Lease lease = store.acquire("runner-a", LEASE);
            assertEquals(Status.NEEDS_ACTION, store.complete(result(
                    "unknown-event", lease, Outcome.RESULT_UNKNOWN, null, "RESULT_UNKNOWN", null)).status());
        }
        clock.advance(Duration.ofHours(3));
        try (TaskStore store = open()) {
            assertEquals(Status.NEEDS_ACTION, store.get(unknown.id()).status());
            assertNull(store.get(unknown.id()).nextRetryAt());
            assertNull(store.acquire("runner-b", LEASE));

            Request nextRequest = new Request("fixture:reconcile", "reconcile/file.txt", "body", "storage-r17");
            Task nextTask = store.create("reconcile-key", nextRequest);
            Lease write = store.acquire("runner-a", Duration.ofSeconds(10));
            clock.advance(Duration.ofSeconds(11));
            assertNull(store.acquire("runner-b", LEASE));
            assertEquals(Status.QUEUED, store.reconcile(result("reconcile-success", write,
                    Outcome.SUCCEEDED, TaskStore.expectedEvidence(nextRequest), null, null)).status());
            Lease verify = store.acquire("runner-b", LEASE);
            assertEquals(Stage.VERIFY_FIXTURE, verify.stage());
            assertEquals("storage-r17", verify.request().storageRevision());
            assertEquals(Status.RUNNING, store.get(nextTask.id()).status());
        }
    }

    @Test void pauseResumeAndCancelConfirmationSurviveStoreRestart() throws Exception {
        Task task;
        Lease lease;
        try (TaskStore store = open()) {
            task = store.create("pause-key", request());
            lease = store.acquire("runner-a", LEASE);
            assertEquals(Status.PAUSING, store.control(task.id(), store.get(task.id()).revision(), Action.PAUSE).status());
        }
        try (TaskStore store = open()) {
            assertEquals(Status.PAUSING, store.get(task.id()).status());
            assertEquals(Status.PAUSED, store.complete(result(
                    "pause-confirm", lease, Outcome.PAUSED, null, null, null)).status());
        }
        try (TaskStore store = open()) {
            Task paused = store.get(task.id());
            assertEquals(Status.QUEUED, store.control(task.id(), paused.revision(), Action.RESUME).status());
            Lease resumed = store.acquire("runner-a", LEASE);
            assertEquals(Stage.WRITE_FIXTURE, resumed.stage());
            assertEquals("storage-r17", resumed.request().storageRevision());
            assertEquals(Status.RUNNING, store.control(task.id(), store.get(task.id()).revision(), Action.CANCEL).status());
            assertNull(store.acquire("runner-b", LEASE));
            assertEquals(Status.CANCELLED, store.complete(result(
                    "cancel-confirm", resumed, Outcome.CANCELLED, null, null, null)).status());
            assertEquals("CANCELLED", operationStatus(task.id(), Stage.WRITE_FIXTURE));
        }
    }

    @Test void successfulPublishRacingCancellationKeepsFactAndSuppressesVerify() throws Exception {
        Request request = new Request("fixture:raced", "raced/file.txt", "content", "storage-r17");
        try (TaskStore store = open()) {
            Task task = store.create("race-key", request);
            Lease write = store.acquire("runner-a", LEASE);
            store.control(task.id(), store.get(task.id()).revision(), Action.CANCEL);
            assertEquals(Status.CANCELLED, store.complete(result("publish-race", write, Outcome.SUCCEEDED,
                    TaskStore.expectedEvidence(request), null, null)).status());
            assertEquals(Status.CANCELLED, store.get(task.id()).status());
            assertEquals("SUCCEEDED", operationStatus(task.id(), Stage.WRITE_FIXTURE));
            assertEquals("CANCELLED", operationStatus(task.id(), Stage.VERIFY_FIXTURE));
            assertNull(store.acquire("runner-b", LEASE));
        }
    }

    @Test void retryAfterIsHonoredAndAutomaticRetriesStopAfterThreeRetries() throws Exception {
        long[] delays = {60_000, 120_000, 600_000};
        try (TaskStore store = open()) {
            Task task = store.create("retry-key", request());
            for (int attempt = 1; attempt <= 4; attempt++) {
                Lease lease = store.acquire("runner-a", LEASE);
                assertEquals(attempt, lease.attempt());
                Duration retryAfter = attempt == 1 ? Duration.ofSeconds(60) : null;
                Ack ack = store.complete(result("retry-" + attempt, lease,
                        Outcome.RETRYABLE_ERROR, null, "TEMPORARY", retryAfter));
                if (attempt < 4) {
                    Task waiting = store.get(task.id());
                    assertEquals(Status.WAITING_RETRY, ack.status());
                    assertEquals(clock.instant().plusMillis(delays[attempt - 1]), waiting.nextRetryAt());
                    clock.advance(Duration.between(clock.instant(), waiting.nextRetryAt()));
                } else {
                    assertEquals(Status.FAILED, ack.status());
                    assertNull(store.get(task.id()).nextRetryAt());
                    assertNull(store.acquire("runner-a", LEASE));
                }
            }
            assertEquals(4, scalar("SELECT attempt FROM operations WHERE stage='WRITE_FIXTURE'"));
            assertEquals(4, scalar("SELECT COUNT(*) FROM results"));
        }
    }

    @Test void heartbeatOrderingConflictsAndReplayAfterCompletionAreStable() throws Exception {
        Request request = request();
        try (TaskStore store = open()) {
            Task task = store.create("heartbeat-key", request);
            Lease write = store.acquire("runner-a", LEASE);
            clock.advance(Duration.ofSeconds(10));
            Heartbeat newest = store.heartbeat(write, "progress-2", 2, Duration.ofSeconds(60));
            clock.advance(Duration.ofSeconds(10));
            assertEquals(newest.expiresAt(),
                    store.heartbeat(write, "progress-1", 1, Duration.ofSeconds(120)).expiresAt());
            assertEquals(newest, store.heartbeat(write, "progress-2", 2, Duration.ofSeconds(60)));
            assertEquals("EVENT_CONFLICT", assertThrows(Conflict.class,
                    () -> store.heartbeat(write, "progress-2", 2, Duration.ofSeconds(61))).getMessage());

            Evidence evidence = TaskStore.expectedEvidence(request);
            store.complete(result("heartbeat-write", write, Outcome.SUCCEEDED, evidence, null, null));
            Lease verify = store.acquire("runner-a", LEASE);
            store.complete(result("heartbeat-verify", verify, Outcome.SUCCEEDED, evidence, null, null));
            assertEquals(Status.SUCCEEDED, store.get(task.id()).status());
            assertEquals(newest, store.heartbeat(write, "progress-2", 2, Duration.ofSeconds(60)));
            assertEquals("EVENT_CONFLICT", assertThrows(Conflict.class,
                    () -> store.heartbeat(write, "progress-2", 2, Duration.ofSeconds(61))).getMessage());
        }
    }

    @Test void concurrentEquivalentCreatesShareOneTaskAndOperation() throws Exception {
        int callers = 12;
        Request request = request();
        try (TaskStore store = open()) {
            ExecutorService pool = Executors.newFixedThreadPool(callers);
            CountDownLatch ready = new CountDownLatch(callers), start = new CountDownLatch(1);
            List<Future<Task>> futures = new ArrayList<>();
            try {
                for (int i = 0; i < callers; i++) {
                    int key = i;
                    futures.add(pool.submit(() -> {
                        ready.countDown();
                        assertTrue(start.await(5, TimeUnit.SECONDS));
                        return store.create("parallel-" + key, request);
                    }));
                }
                assertTrue(ready.await(5, TimeUnit.SECONDS));
                start.countDown();
                String id = futures.getFirst().get(5, TimeUnit.SECONDS).id();
                for (Future<Task> future : futures) assertEquals(id, future.get(5, TimeUnit.SECONDS).id());
                assertEquals(1, scalar("SELECT COUNT(*) FROM tasks"));
                assertEquals(1, scalar("SELECT COUNT(*) FROM operations"));
                assertEquals(callers, scalar("SELECT COUNT(*) FROM requests"));
            } finally {
                pool.shutdownNow();
                assertTrue(pool.awaitTermination(5, TimeUnit.SECONDS));
            }
        }
    }

    private TaskStore open() throws SQLException, IOException { return new TaskStore(database, clock); }
    private Request request() { return new Request("fixture:resource-17", "archive/fixture.txt", "fixture content", "storage-r17"); }
    private static Request changed(Request r) { return new Request(r.resourceKey(), r.relativePath(), "changed", r.storageRevision()); }
    private static Result result(String id, Lease lease, Outcome outcome, Evidence evidence, String error, Duration delay) {
        return new Result(id, lease, outcome, evidence, error, delay);
    }
    private static Lease withRunner(Lease l, String runner) {
        return new Lease(l.operationId(), l.taskId(), l.stage(), l.attempt(), l.fencingToken(),
                l.leaseId(), runner, l.expiresAt(), l.request());
    }
    private Connection connection() throws SQLException { return DriverManager.getConnection("jdbc:sqlite:" + database.toAbsolutePath()); }
    private int scalar(String sql) throws SQLException {
        try (Connection c = connection(); Statement s = c.createStatement(); ResultSet r = s.executeQuery(sql)) {
            assertTrue(r.next()); return r.getInt(1);
        }
    }
    private String operationStatus(String taskId, Stage stage) throws SQLException {
        try (Connection c = connection(); PreparedStatement s = c.prepareStatement(
                "SELECT status FROM operations WHERE task_id=? AND stage=?")) {
            s.setString(1, taskId); s.setString(2, stage.name());
            try (ResultSet r = s.executeQuery()) { assertTrue(r.next()); return r.getString(1); }
        }
    }

    private static final class MutableClock extends Clock {
        private Instant now;
        private MutableClock(Instant now) { this.now = now; }
        @Override public ZoneId getZone() { return ZoneOffset.UTC; }
        @Override public Clock withZone(ZoneId zone) { return new MutableClock(now); }
        @Override public Instant instant() { return now; }
        private void advance(Duration duration) { now = now.plus(duration); }
    }
}
