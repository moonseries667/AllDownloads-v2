package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.List;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class PlatformClientFlowTest {
    @TempDir Path temp;
    BusinessStore store;
    BusinessApplication app;
    JsonNode examples;
    MutableClock clock = new MutableClock();

    @BeforeEach void open() throws Exception {
        examples = MAPPER.readTree(Path.of("../../contracts/feedback2.examples.json").toFile());
        store = new BusinessStore(temp.resolve("control.db"), clock);
        app = new BusinessApplication(store, temp.resolve("default").toString());
        store.tx(() -> app.queue.register(object(
            "runnerId", "fixture",
            "protocolVersion", 1,
            "capabilities", List.of("VALIDATE_TARGET", "TRANSFER_QUARK_ITEMS", "RECONCILE_QUARK_TRANSFER", "LIST_QUARK_TREE", "DOWNLOAD_MEDIA"),
            "tools", object())));
        JsonNode validation = acquire("VALIDATE_TARGET");
        if (!validation.isNull()) finish(validation, "SUCCEEDED", object(
            "targetId", "default", "rootId", "default-root",
            "canonicalPath", temp.resolve("default").toString(), "writable", true), null);
        store.tx(() -> { app.feedback.quark.acceptTree((ObjectNode) examples.path("quarkRoot").deepCopy()); return null; });
    }

    @AfterEach void close() throws Exception { if (store != null) store.close(); }

    JsonNode acquire(String type) {
        return store.tx(() -> app.queue.acquire(object("runnerId", "fixture", "capabilities", List.of(type))).path("operation"));
    }

    ObjectNode report(JsonNode operation, String status, JsonNode result, JsonNode error) {
        return object(
            "eventId", id(), "runnerId", operation.path("runnerId"),
            "leaseId", operation.path("leaseId"), "attempt", operation.path("attempt"),
            "fencingToken", operation.path("fencingToken"), "status", status,
            "result", result, "error", error);
    }

    ObjectNode finish(JsonNode operation, String status, JsonNode result, JsonNode error) {
        ObjectNode request = report(operation, status, result, error);
        return store.tx(() -> app.queue.result(text(operation, "operationId"), request, false));
    }

    String seedShare() {
        ObjectNode request = (ObjectNode) examples.path("quarkSharedTarget").deepCopy();
        ((ArrayNode) request.path("entries")).remove(1);
        ObjectNode accepted = store.tx(() -> app.feedback.addShares(request));
        return text(accepted.path("items").get(0), "shareId");
    }

    ObjectNode transferFacts(JsonNode operation, boolean complete, boolean safeToResume, JsonNode nodes) {
        JsonNode payload = operation.path("payload");
        return object(
            "shareId", payload.path("shareId"),
            "targetFolderId", payload.path("targetFolderId"),
            "targetFolderRevision", payload.path("targetFolderRevision"),
            "complete", complete, "safeToResume", safeToResume, "nodes", nodes);
    }

    ObjectNode fileNode(String shareId, String id, String version) {
        return object(
            "id", id, "shareId", shareId, "parentId", null, "externalId", "external-" + id,
            "version", version, "name", id + ".mp4", "kind", "FILE",
            "relativePath", "课程/" + id + ".mp4", "sizeBytes", "7", "modifiedAt", null,
            "availability", "AVAILABLE", "availabilityReason", null,
            "downloadSummary", object("state", "NOT_REQUESTED"));
    }

    int countOperations(String type) {
        return store.tx(() -> store.rows("SELECT id FROM operations WHERE type=?", type).size());
    }

    @Test void quarkWatchSchedulesOnlyTransfersAndManualRefreshMergesWithoutChangingTarget() {
        String shareId = seedShare();
        JsonNode first = acquire("TRANSFER_QUARK_ITEMS");
        assertEquals("TRANSFER_QUARK_ITEMS", text(first, "operationType"));
        assertEquals("quark-test", text(first, "accountRef"));
        ObjectNode initialShare = store.tx(() -> app.feedback.quark.share(shareId));
        JsonNode frozenTarget = initialShare.path("target").deepCopy();
        assertEquals("folder-a", text(first.path("payload"), "targetFolderId"));
        assertEquals("a-v1", text(first.path("payload"), "targetFolderRevision"));

        finish(first, "SUCCEEDED", transferFacts(first, true, false, array(List.of(fileNode(shareId, "file", "v1")))), null);
        assertEquals("TRANSFERRED", text(store.tx(() -> app.feedback.quark.share(shareId)), "status"));
        assertEquals(0, store.tx(() -> store.all("order").size()));
        assertEquals(0, countOperations("DOWNLOAD_MEDIA"));

        ObjectNode changedTree = (ObjectNode) examples.path("quarkRoot").deepCopy();
        ((ObjectNode) changedTree.path("folders").get(1)).put("revision", "a-v2");
        store.tx(() -> { app.feedback.quark.acceptTree(changedTree); return null; });
        clock.instant = clock.instant.plusSeconds(3601);
        app.feedback.tick();
        assertEquals(2, countOperations("TRANSFER_QUARK_ITEMS"));
        assertEquals(0, store.tx(() -> store.all("order").size()));
        assertEquals(0, countOperations("DOWNLOAD_MEDIA"));

        ObjectNode firstRefresh = store.tx(() -> app.feedback.refreshShare(shareId));
        ObjectNode mergedRefresh = store.tx(() -> app.feedback.refreshShare(shareId));
        assertEquals(firstRefresh.path("taskId"), mergedRefresh.path("taskId"));
        assertEquals(2, countOperations("TRANSFER_QUARK_ITEMS"));
        JsonNode scheduled = acquire("TRANSFER_QUARK_ITEMS");
        assertEquals(firstRefresh.path("taskId"), scheduled.path("taskId"));
        assertEquals("folder-a", text(scheduled.path("payload"), "targetFolderId"));
        assertEquals("a-v1", text(scheduled.path("payload"), "targetFolderRevision"));
        finish(scheduled, "SUCCEEDED", transferFacts(scheduled, true, false, array(List.of(fileNode(shareId, "file", "v2")))), null);

        ObjectNode currentShare = store.tx(() -> app.feedback.quark.share(shareId));
        store.tx(() -> app.feedback.quark.setWatcher(shareId, false, text(currentShare, "revision")));
        clock.instant = clock.instant.plusSeconds(3601);
        app.feedback.tick();
        assertEquals(2, countOperations("TRANSFER_QUARK_ITEMS"));
        assertEquals(0, store.tx(() -> store.all("order").size()));
        assertEquals(0, countOperations("DOWNLOAD_MEDIA"));
        ObjectNode after = store.tx(() -> app.feedback.quark.share(shareId));
        assertEquals(frozenTarget, after.path("target"));
        assertEquals("folder-a", text(after, "targetFolderId"));
        assertEquals("a-v1", text(after, "targetFolderRevision"));
        assertFalse(after.path("watchEnabled").asBoolean());
    }

    @Test void unknownTransferRequiresReconcileAndOnlySafeProofResumesTheOriginalOperation() {
        String shareId = seedShare();
        JsonNode original = acquire("TRANSFER_QUARK_ITEMS");
        finish(original, "RESULT_UNKNOWN", object("files", List.of()), object(
            "code", "RESULT_UNKNOWN", "completedEffects", List.of(), "pendingEffects", List.of()));
        String originalTask = text(original, "taskId");
        JsonNode blockedTask = store.tx(() -> store.get("task", originalTask));
        assertEquals("NEEDS_ACTION", text(blockedTask, "status"));
        assertEquals(List.of("RECONCILE"), strings(blockedTask.path("allowedActions")));
        assertFalse(strings(blockedTask.path("allowedActions")).contains("RETRY"));
        String issueId = text(store.tx(() -> app.feedback.issues.list(false)).get(0), "id");
        assertEquals("RECONCILE_REQUIRED", assertThrows(Failure.class, () -> store.tx(
            () -> {app.queue.requeueReadOnlyReconciliation(originalTask);return null;})).code);

        store.tx(() -> app.queue.control(originalTask, text(blockedTask, "revision"), "RECONCILE"));
        JsonNode reconcile = acquire("RECONCILE_QUARK_TRANSFER");
        assertEquals("quark-test", text(reconcile, "accountRef"));
        assertEquals(text(original, "operationId"), text(reconcile.path("payload"), "originalOperationId"));
        assertEquals(original.path("payload").path("shareId"), reconcile.path("payload").path("shareId"));
        assertEquals(original.path("payload").path("targetFolderId"), reconcile.path("payload").path("targetFolderId"));
        assertEquals(original.path("payload").path("targetFolderRevision"), reconcile.path("payload").path("targetFolderRevision"));
        assertEquals("NEEDS_ACTION", text(store.tx(() -> store.get("task", originalTask)), "status"));

        finish(reconcile, "RESULT_UNKNOWN", object("complete", false, "safeToResume", false), object(
            "code", "RESULT_UNKNOWN", "completedEffects", List.of(), "pendingEffects", List.of()));
        String reconcileTask = text(reconcile, "taskId");
        JsonNode failedReconcile = store.tx(() -> store.get("task", reconcileTask));
        assertEquals("NEEDS_ACTION", text(failedReconcile, "status"));
        assertEquals("NEEDS_ACTION", store.tx(() -> store.row("SELECT status FROM operations WHERE id=?", text(reconcile, "operationId")))[0]);
        assertEquals("NEEDS_ACTION", store.tx(() -> store.row("SELECT status FROM operations WHERE id=?", text(original, "operationId")))[0]);

        JsonNode originalAfterFailure = store.tx(() -> store.get("task", originalTask));
        store.tx(() -> app.queue.control(originalTask, text(originalAfterFailure, "revision"), "RECONCILE"));
        assertEquals("QUEUED", text(store.tx(() -> store.get("task", reconcileTask)), "status"));
        JsonNode retriedReconcile = acquire("RECONCILE_QUARK_TRANSFER");
        assertEquals(reconcile.path("operationId"), retriedReconcile.path("operationId"));
        assertEquals(2, retriedReconcile.path("attempt").asInt());
        assertEquals(1, countOperations("RECONCILE_QUARK_TRANSFER"));
        assertEquals(1, countOperations("TRANSFER_QUARK_ITEMS"));

        ObjectNode unsafe = transferFacts(retriedReconcile, false, false, array(List.of()));
        assertEquals("RESULT_UNKNOWN", assertThrows(Failure.class, () -> finish(retriedReconcile, "SUCCEEDED", unsafe, null)).code);
        assertEquals("NEEDS_ACTION", text(store.tx(() -> store.get("task", originalTask)), "status"));

        ObjectNode safe = transferFacts(retriedReconcile, false, true, array(List.of()));
        finish(retriedReconcile, "SUCCEEDED", safe, null);
        assertEquals("QUEUED", text(store.tx(() -> store.get("task", originalTask)), "status"));
        assertEquals("QUEUED", store.tx(() -> store.row("SELECT status FROM operations WHERE id=?", text(original, "operationId")))[0]);
        assertEquals(1, countOperations("LIST_QUARK_TREE"));
        JsonNode resumed = acquire("TRANSFER_QUARK_ITEMS");
        assertEquals(original.path("operationId"), resumed.path("operationId"));
        assertEquals(original.path("payload"), resumed.path("payload"));
        assertEquals(1, countOperations("TRANSFER_QUARK_ITEMS"));

        finish(resumed, "SUCCEEDED", transferFacts(resumed, true, false, array(List.of(fileNode(shareId, "file", "v1")))), null);
        assertEquals("SUCCEEDED", text(store.tx(() -> store.get("task", originalTask)), "status"));
        assertTrue(store.tx(() -> app.feedback.issues.get(issueId)).path("resolved").asBoolean());
    }

    @Test void completeReconciliationConfirmsOriginalTaskAndResolvesItsIssue() {
        String shareId = seedShare();
        JsonNode original = acquire("TRANSFER_QUARK_ITEMS");
        finish(original, "RESULT_UNKNOWN", object("files", List.of()), object(
            "code", "RESULT_UNKNOWN", "completedEffects", List.of(), "pendingEffects", List.of()));
        String originalTask = text(original, "taskId");
        String issueId = text(store.tx(() -> app.feedback.issues.list(false)).get(0), "id");
        JsonNode task = store.tx(() -> store.get("task", originalTask));
        store.tx(() -> app.queue.control(originalTask, text(task, "revision"), "RECONCILE"));
        JsonNode reconcile = acquire("RECONCILE_QUARK_TRANSFER");
        finish(reconcile, "SUCCEEDED", transferFacts(reconcile, true, false, array(List.of(fileNode(shareId, "file", "v1")))), null);

        assertEquals("SUCCEEDED", text(store.tx(() -> store.get("task", originalTask)), "status"));
        assertEquals("SUCCEEDED", store.tx(() -> store.row("SELECT status FROM operations WHERE id=?", text(original, "operationId")))[0]);
        assertTrue(store.tx(() -> app.feedback.issues.get(issueId)).path("resolved").asBoolean());
        assertEquals("TRANSFERRED", text(store.tx(() -> app.feedback.quark.share(shareId)), "status"));
    }

    @Test void safeReconciliationDoesNotReviveTheCancelledTransferIntent() {
        seedShare();
        JsonNode original = acquire("TRANSFER_QUARK_ITEMS");
        String taskId = text(original, "taskId");
        JsonNode runningTask = store.tx(() -> store.get("task", taskId));
        store.tx(() -> app.queue.control(taskId, text(runningTask, "revision"), "CANCEL"));
        finish(original, "RESULT_UNKNOWN", object("files", List.of()), object(
            "code", "RESULT_UNKNOWN", "completedEffects", List.of(), "pendingEffects", List.of()));
        JsonNode blocked = store.tx(() -> store.get("task", taskId));
        assertEquals("CANCEL", text(blocked, "desiredAction"));
        store.tx(() -> app.queue.control(taskId, text(blocked, "revision"), "RECONCILE"));
        JsonNode reconcile = acquire("RECONCILE_QUARK_TRANSFER");
        finish(reconcile, "SUCCEEDED", transferFacts(reconcile, false, true, array(List.of())), null);

        assertEquals("CANCELLED", text(store.tx(() -> store.get("task", taskId)), "status"));
        assertEquals("CANCELLED", store.tx(() -> store.row("SELECT status FROM operations WHERE id=?", text(original, "operationId")))[0]);
        assertEquals(1, countOperations("TRANSFER_QUARK_ITEMS"));
        assertEquals(1, countOperations("LIST_QUARK_TREE"));
        assertEquals(0, store.tx(() -> store.all("order").size()));
        assertEquals(0, countOperations("DOWNLOAD_MEDIA"));
        assertTrue(acquire("TRANSFER_QUARK_ITEMS").isNull());
    }

    ObjectNode factsRequest(JsonNode operation, String eventId, long sequence, JsonNode facts) {
        return object(
            "eventId", eventId, "runnerId", operation.path("runnerId"),
            "leaseId", operation.path("leaseId"), "attempt", operation.path("attempt"),
            "fencingToken", operation.path("fencingToken"), "sequence", sequence, "facts", facts);
    }

    @Test void factsSequenceIsNonNegativeOrderedAndIdempotentByUnchangedEventContent() {
        seedShare();
        JsonNode operation = acquire("TRANSFER_QUARK_ITEMS");
        ObjectNode negative = factsRequest(operation, "facts-negative", -1, object("phase", "probe"));
        assertEquals("INVALID_INPUT", assertThrows(Failure.class, () -> store.tx(
            () -> app.queue.facts(text(operation, "operationId"), negative))).code);

        ObjectNode first = factsRequest(operation, "facts-0", 0, object("phase", "metadata"));
        ObjectNode originalAck = store.tx(() -> app.queue.facts(text(operation, "operationId"), first));
        assertEquals(originalAck, store.tx(() -> app.queue.facts(text(operation, "operationId"), first)));
        ObjectNode changed = first.deepCopy();
        changed.set("facts", object("phase", "different-content"));
        assertEquals("EVENT_CONFLICT", assertThrows(Failure.class, () -> store.tx(
            () -> app.queue.facts(text(operation, "operationId"), changed))).code);

        ObjectNode later = factsRequest(operation, "facts-3", 3, object("phase", "files"));
        store.tx(() -> app.queue.facts(text(operation, "operationId"), later));
        ObjectNode outOfOrder = factsRequest(operation, "facts-2", 2, object("phase", "older"));
        assertEquals("FACTS_OUT_OF_ORDER", assertThrows(Failure.class, () -> store.tx(
            () -> app.queue.facts(text(operation, "operationId"), outOfOrder))).code);
    }

    static final class MutableClock extends Clock {
        Instant instant = Instant.parse("2026-10-15T10:00:00Z");
        public ZoneId getZone() { return ZoneOffset.UTC; }
        public Clock withZone(ZoneId zone) { return this; }
        public Instant instant() { return instant; }
    }
}
