package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.sql.SQLException;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** A user-requested read-only observation of the original upload's immutable plan. */
public final class ArchiveRecovery {
    private final BusinessStore store;
    private final OperationQueue queue;
    private final DownloadManagement management;
    public ArchiveRecovery(BusinessStore store,OperationQueue queue,DownloadManagement management) {this.store=store;this.queue=queue;this.management=management;}

    public void request(JsonNode original) throws SQLException {
        String originalId=required(original,"operationId");
        String[] row=store.row("SELECT body,status,attempt,fence FROM operations WHERE id=? AND type='UPLOAD_ARTIFACT'",originalId);
        if(row==null||!row[1].equals("NEEDS_ACTION"))throw Failure.conflict("RECONCILE_REQUIRED");
        JsonNode frozen=parse(row[0]);
        // Only an operation which can still be leased is reused. Completed/failed observations
        // remain history; a subsequent explicit request gets a fresh read-only operation.
        for(String[] active:store.rows("SELECT task_id FROM operations WHERE type='VERIFY_ARCHIVE' AND json_extract(body,'$.payload.originalOperationId')=? AND status IN ('QUEUED','RUNNING','WAITING_RETRY')",originalId)) {
            ObjectNode proof=store.optional("archiveVerification",active[0]);
            if(proof!=null&&text(proof,"attempt").equals(row[2])&&text(proof,"fence").equals(row[3]))return;
        }
        String task=queue.enqueue("VERIFY_ARCHIVE",text(frozen,"platform"),"archive-verify:"+originalId+":"+id(),frozen.path("destination"),text(frozen,"storageRevision"),
            object("schemaVersion",1,"originalOperationId",originalId,"upload",frozen.path("payload")));
        queue.account(task,frozen.path("accountRef").isNull()?null:text(frozen,"accountRef"));
        store.put("archiveVerification",task,object("originalOperationId",originalId,"attempt",row[2],"fence",row[3]));
    }

    public void complete(JsonNode operation,JsonNode result) throws SQLException {
        if(!result.isObject()||result.size()!=5||!result.has("archive")||!result.has("reasonCode"))throw Failure.conflict("VERIFICATION_FAILED");
        Set<String> fields=Set.of("originalOperationId","complete","safeToResume","archive","reasonCode");
        result.fieldNames().forEachRemaining(field->{if(!fields.contains(field))throw Failure.conflict("VERIFICATION_FAILED");});
        JsonNode payload=operation.path("payload");String originalId=required(payload,"originalOperationId");
        if(!originalId.equals(required(result,"originalOperationId"))||!result.path("complete").isBoolean()||!result.path("safeToResume").isBoolean())throw Failure.conflict("VERIFICATION_FAILED");
        boolean complete=result.path("complete").asBoolean(),safe=result.path("safeToResume").asBoolean();
        if(complete&&(safe||!result.path("archive").isObject()||!result.path("reasonCode").isNull())||!complete&&(!result.path("archive").isNull()||!result.path("reasonCode").isTextual()||!text(result,"reasonCode").matches("[A-Z][A-Z0-9_]{0,79}")))throw Failure.conflict("VERIFICATION_FAILED");
        if(complete) {
            JsonNode archive=result.path("archive");exactFields(archive,Set.of("projectId","archiveTarget","files","verified"));
            if(!archive.path("verified").isBoolean()||!archive.path("verified").asBoolean()||!archive.path("files").isArray()||archive.path("files").isEmpty())throw Failure.conflict("VERIFICATION_FAILED");
            for(JsonNode file:archive.path("files")) {
                exactFields(file,Set.of("outputId","relativePath","sizeBytes","sha256","remoteId","remoteName","verified"));
                if(!file.path("verified").isBoolean()||!file.path("verified").asBoolean())throw Failure.conflict("VERIFICATION_FAILED");
            }
        }
        String[] row=store.row("SELECT body,status,attempt,fence FROM operations WHERE id=? AND type='UPLOAD_ARTIFACT'",originalId);
        if(row==null)throw Failure.conflict("VERIFICATION_FAILED");JsonNode original=parse(row[0]);
        if(payload.size()!=3||payload.path("schemaVersion").asInt()!=1||!payload.path("upload").equals(original.path("payload")))throw Failure.conflict("VERIFICATION_FAILED");
        for(String field:List.of("platform","accountRef","destination","storageRevision"))if(!operation.path(field).equals(original.path(field)))throw Failure.conflict("VERIFICATION_FAILED");
        ObjectNode proof=store.get("archiveVerification",text(operation,"taskId"));
        if(!text(proof,"originalOperationId").equals(originalId))throw Failure.conflict("VERIFICATION_FAILED");
        // An older observation cannot requeue or finish a later execution attempt. It is still
        // acknowledged as delivered, so the runner does not resend a stale result indefinitely.
        if(!row[1].equals("NEEDS_ACTION")||!text(proof,"attempt").equals(row[2])||!text(proof,"fence").equals(row[3]))return;
        if(complete)management.uploadResult(original,result.path("archive"));
        queue.confirmedArchive(originalId,complete,safe,complete?null:text(result,"reasonCode"));
    }
    private static void exactFields(JsonNode node,Set<String> fields) {
        if(!node.isObject()||node.size()!=fields.size())throw Failure.conflict("VERIFICATION_FAILED");
        node.fieldNames().forEachRemaining(field->{if(!fields.contains(field))throw Failure.conflict("VERIFICATION_FAILED");});
    }
}
