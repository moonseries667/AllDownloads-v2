package dev.alldownloads.core;

import java.time.Duration;
import java.time.Instant;

/** Module-private domain types; these are not the pending #2 HTTP contract. */
public final class Model {
    private Model() {}
    public enum Status { QUEUED, RUNNING, PAUSING, PAUSED, WAITING_RETRY, NEEDS_ACTION, SUCCEEDED, FAILED, CANCELLED }
    public enum Action { CONTINUE, PAUSE, CANCEL, RESUME, RETRY }
    public enum Stage { WRITE_FIXTURE, VERIFY_FIXTURE }
    public enum Outcome { SUCCEEDED, PAUSED, CANCELLED, RETRYABLE_ERROR, NEEDS_ACTION, RESULT_UNKNOWN }
    public record Request(String resourceKey, String relativePath, String content, String storageRevision) {}
    public record Task(String id, Request request, Status status, Action desiredAction, long revision,
                       String errorCode, Instant nextRetryAt) {}
    public record Lease(String operationId, String taskId, Stage stage, int attempt, long fencingToken,
                        String leaseId, String runnerId, Instant expiresAt, Request request) {}
    public record Evidence(String relativePath, String sizeBytes, String sha256) {}
    public record Result(String eventId, Lease lease, Outcome outcome, Evidence evidence,
                         String errorCode, Duration retryAfter) {}
    public record Ack(String id, String eventId, String taskId, Status status) {}
    public record Heartbeat(Action desiredAction, Instant expiresAt) {}
    public static final class Conflict extends RuntimeException {
        public Conflict(String message) { super(message); }
    }
}
