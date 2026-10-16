package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.file.Path;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class ArchiveRecoveryTest {
    @TempDir Path temp;
    BusinessFlowTest f;
    static final List<String> CAPS=List.of("UPLOAD_ARTIFACT","VERIFY_ARCHIVE","FETCH_BIL_METADATA","AUTH_STEP");
    @BeforeEach void start() throws Exception {f=new BusinessFlowTest();f.temp=temp;f.open();register();}
    @AfterEach void close() throws Exception {f.close();}
    void register() {f.store.tx(()->f.app.queue.register(object("runnerId","runner","protocolVersion",1,"capabilities",CAPS,"tools",object())));}
    JsonNode upload() {
        f.store.tx(()->{f.store.put("settings","global",object("revision","1","archiveDestination",object("accountId","drive-main","folderId","frozen-folder","revision","v1","name","目录","path","根/目录")));return null;});
        f.store.tx(()->f.app.queue.register(object("runnerId","runner","protocolVersion",1,"capabilities",List.of("DOWNLOAD_MEDIA"),"tools",object())));
        f.submit(f.preview(f.request("audio-fixture")));JsonNode download=f.acquire("DOWNLOAD_MEDIA");f.complete(download,f.fileFacts(download));register();
        f.store.tx(()->{String task=f.store.row("SELECT task_id FROM operations WHERE type='UPLOAD_ARTIFACT'")[0];f.app.queue.account(task,"drive-main");return null;});
        return f.acquire("UPLOAD_ARTIFACT");
    }
    JsonNode task(JsonNode operation) {return f.store.tx(()->f.store.get("task",text(operation,"taskId")));}
    ObjectNode action(JsonNode operation,String action) {return f.store.tx(()->f.app.queue.control(text(operation,"taskId"),text(f.store.get("task",text(operation,"taskId")),"revision"),action));}
    ObjectNode fail(JsonNode operation,String status,String code,JsonNode completed,JsonNode pending) {
        ObjectNode request=f.result(operation,null);request.put("status",status);request.set("error",object("code",code,"completedEffects",completed,"pendingEffects",pending));f.store.tx(()->f.app.queue.result(text(operation,"operationId"),request,false));return request;
    }
    void unknown(JsonNode operation) {fail(operation,"RESULT_UNKNOWN","RESULT_UNKNOWN",array(List.of()),array(List.of()));}
    ObjectNode archive(JsonNode original) {
        List<JsonNode> files=new ArrayList<>();for(JsonNode file:original.path("payload").path("files")) {ObjectNode remote=(ObjectNode)file.deepCopy();remote.remove("mediaKinds");String path=text(file,"relativePath");remote.put("remoteId","remote-"+text(file,"outputId"));remote.put("remoteName",path.substring(path.lastIndexOf('/')+1));files.add(remote);}
        return object("projectId",original.path("payload").path("projectId"),"archiveTarget",original.path("payload").path("archiveTarget"),"files",files,"verified",true);
    }
    ObjectNode observation(JsonNode original,boolean complete,boolean safe,String reason) {return object("originalOperationId",original.path("operationId"),"complete",complete,"safeToResume",safe,"archive",complete?archive(original):null,"reasonCode",reason);}
    int count(String type) {return f.store.tx(()->f.store.rows("SELECT id FROM operations WHERE type=?",type).size());}

    @Test void completeObservationCopiesFrozenPlanConfirmsOriginalAndDeduplicatesItsResult() throws Exception {
        JsonNode upload=upload();unknown(upload);String issueId=text(list(f.store.tx(()->f.app.feedback.issues.list(false))).stream().filter(issue->text(issue,"taskId").equals(text(upload,"taskId"))).findFirst().orElseThrow(),"id");assertEquals(List.of("RECONCILE"),strings(task(upload).path("allowedActions")));assertThrows(Failure.class,()->action(upload,"RETRY"));
        f.store.tx(()->{f.store.put("settings","global",object("revision","2","archiveDestination",object("accountId","different-account","folderId","different-folder")));return null;});
        action(upload,"RECONCILE");action(upload,"RECONCILE");assertEquals(1,count("VERIFY_ARCHIVE"));JsonNode verify=f.acquire("VERIFY_ARCHIVE");
        for(String field:List.of("accountRef","destination","storageRevision","platform"))assertEquals(upload.path(field),verify.path(field));
        assertEquals(object("schemaVersion",1,"originalOperationId",upload.path("operationId"),"upload",upload.path("payload")),verify.path("payload"));assertEquals("VERIFY",text(task(verify),"kind"));
        ObjectNode result=f.result(verify,observation(upload,true,false,null));JsonNode ack=f.store.tx(()->f.app.queue.result(text(verify,"operationId"),result,false));assertEquals(ack,f.store.tx(()->f.app.queue.result(text(verify,"operationId"),result,false)));
        assertEquals("SUCCEEDED",text(task(upload),"status"));assertTrue(f.store.tx(()->f.store.all("file")).stream().allMatch(file->text(file,"archiveState").equals("VERIFIED")));assertTrue(f.store.tx(()->f.app.feedback.issues.get(issueId)).path("resolved").asBoolean());
        assertEquals(1,count("UPLOAD_ARTIFACT"));assertEquals(1,count("DOWNLOAD_MEDIA"));assertTrue(f.acquire("UPLOAD_ARTIFACT").isNull());
        java.nio.file.Files.writeString(Path.of("target/archive-recovery-captures.json"),object("payload",verify.path("payload"),"complete",result.path("result"),"safe",observation(upload,false,true,"ARCHIVE_INCOMPLETE"),"unknown",observation(upload,false,false,"ARCHIVE_PLAN_MISSING")).toPrettyString());
    }
    @ParameterizedTest @ValueSource(strings={"CONTINUE","PAUSE","CANCEL"}) void safeProofRequeuesOnlyTheOriginalAndHonorsItsControlIntent(String desired) {
        JsonNode upload=upload();if(!desired.equals("CONTINUE"))action(upload,desired);unknown(upload);action(upload,"RECONCILE");JsonNode verify=f.acquire("VERIFY_ARCHIVE");f.complete(verify,observation(upload,false,true,"ARCHIVE_INCOMPLETE"));
        String expected=desired.equals("CONTINUE")?"QUEUED":desired.equals("PAUSE")?"PAUSED":"CANCELLED";assertEquals(expected,text(task(upload),"status"));assertEquals(desired,text(task(upload),"desiredAction"));
        assertEquals(1,count("UPLOAD_ARTIFACT"));assertEquals(1,count("DOWNLOAD_MEDIA"));JsonNode resumed=f.acquire("UPLOAD_ARTIFACT");
        if(desired.equals("CONTINUE")) {assertEquals(upload.path("operationId"),resumed.path("operationId"));assertEquals(2,resumed.path("attempt").asInt());for(String field:List.of("payload","destination","storageRevision","accountRef"))assertEquals(upload.path(field),resumed.path(field));}
        else assertTrue(resumed.isNull());
    }
    @Test void failedAndInconclusiveObservationsCanBeRequestedAgainAcrossRestart() throws Exception {
        JsonNode upload=upload();unknown(upload);action(upload,"RECONCILE");JsonNode first=f.acquire("VERIFY_ARCHIVE");f.complete(first,observation(upload,false,false,"ARCHIVE_PLAN_MISSING"));assertEquals("NEEDS_ACTION",text(task(upload),"status"));assertEquals("ARCHIVE_PLAN_MISSING",text(task(upload).path("error"),"code"));assertEquals("SUCCEEDED",text(task(first),"status"));
        f.store.close();f.start();register();action(upload,"RECONCILE");JsonNode second=f.acquire("VERIFY_ARCHIVE");assertNotEquals(first.path("operationId"),second.path("operationId"));fail(second,"NEEDS_ACTION","NETWORK_TEMPORARY",array(List.of()),array(List.of()));
        assertTrue(strings(task(second).path("allowedActions")).contains("RETRY"));action(second,"RETRY");JsonNode retried=f.acquire("VERIFY_ARCHIVE");assertEquals(second.path("operationId"),retried.path("operationId"));assertEquals(2,retried.path("attempt").asInt());fail(retried,"NEEDS_ACTION","ACCOUNT_PERMISSION_DENIED",array(List.of()),array(List.of()));
        action(upload,"RECONCILE");JsonNode third=f.acquire("VERIFY_ARCHIVE");assertNotEquals(second.path("operationId"),third.path("operationId"));f.complete(third,observation(upload,true,false,null));assertEquals("SUCCEEDED",text(task(upload),"status"));assertEquals(1,count("UPLOAD_ARTIFACT"));
    }
    @Test void invalidProofAndIncompleteArchiveCannotCommitAnyBusinessFacts() {
        JsonNode upload=upload();unknown(upload);action(upload,"RECONCILE");JsonNode verify=f.acquire("VERIFY_ARCHIVE");
        ObjectNode wrong=observation(upload,true,false,null);wrong.put("originalOperationId","another-upload");assertThrows(Failure.class,()->f.complete(verify,wrong));
        ObjectNode contradictory=observation(upload,true,true,null);assertThrows(Failure.class,()->f.complete(verify,contradictory));
        ObjectNode partial=observation(upload,false,false,"ARCHIVE_INCOMPLETE");partial.set("archive",archive(upload));assertThrows(Failure.class,()->f.complete(verify,partial));
        ObjectNode incomplete=observation(upload,true,false,null);((ObjectNode)incomplete.path("archive")).set("files",array(List.of()));assertThrows(Failure.class,()->f.complete(verify,incomplete));
        ObjectNode wrongType=observation(upload,true,false,null);((ObjectNode)wrongType.path("archive")).put("verified","true");assertThrows(Failure.class,()->f.complete(verify,wrongType));
        ObjectNode extra=observation(upload,true,false,null);((ObjectNode)extra.path("archive").path("files").get(0)).put("extraProviderField","unexpected");assertThrows(Failure.class,()->f.complete(verify,extra));
        assertEquals("NEEDS_ACTION",text(task(upload),"status"));assertEquals("RUNNING",text(task(verify),"status"));assertTrue(f.store.tx(()->f.store.all("file")).stream().noneMatch(file->text(file,"archiveState").equals("VERIFIED")));
        f.complete(verify,observation(upload,true,false,null));assertEquals("SUCCEEDED",text(task(upload),"status"));
    }
    @Test void oldObservationCannotFinishOrResumeALaterUploadAttempt() {
        JsonNode upload=upload();unknown(upload);action(upload,"RECONCILE");JsonNode old=f.acquire("VERIFY_ARCHIVE");fail(old,"NEEDS_ACTION","NETWORK_TEMPORARY",array(List.of()),array(List.of()));
        // Retrying an earlier read and requesting a fresh read can overlap without either
        // observation being allowed to overwrite a later upload attempt.
        action(upload,"RECONCILE");action(old,"RETRY");JsonNode first=f.acquire("VERIFY_ARCHIVE"),second=f.acquire("VERIFY_ARCHIVE");assertFalse(first.isNull());assertFalse(second.isNull());
        f.complete(first,observation(upload,false,true,"ARCHIVE_INCOMPLETE"));JsonNode resumed=f.acquire("UPLOAD_ARTIFACT");unknown(resumed);
        f.complete(second,observation(upload,true,false,null));assertEquals("NEEDS_ACTION",text(task(upload),"status"));assertTrue(f.store.tx(()->f.store.all("file")).stream().noneMatch(file->text(file,"archiveState").equals("VERIFIED")));
        action(upload,"RECONCILE");JsonNode fresh=f.acquire("VERIFY_ARCHIVE");f.complete(fresh,observation(resumed,true,false,null));assertEquals("SUCCEEDED",text(task(upload),"status"));
    }
    @Test void knownPreUploadFailuresWithExplicitlyEmptyEffectsAllowOnlyUserRetry() {
        JsonNode upload=upload();JsonNode current=upload;
        for(String status:List.of("NEEDS_ACTION","RETRYABLE_ERROR"))for(String code:List.of("AUTH_REQUIRED","ACCOUNT_NOT_CONFIGURED","AUTH_RCLONE_FAILED","NETWORK_TEMPORARY","OPAQUE_CHECK_FAILED")) {
            ObjectNode report=fail(current,status,code,array(List.of()),array(List.of()));ObjectNode proof=f.store.tx(()->f.store.get("knownUploadFailure",text(upload,"taskId")));assertEquals(code,text(proof,"code"));assertEquals(report.path("eventId"),proof.path("eventId"));assertEquals(code,text(task(current).path("error"),"code"));
            assertEquals(Set.of("RECONCILE","RETRY"),new HashSet<>(strings(task(current).path("allowedActions"))));f.clock.now=f.clock.now.plusSeconds(601);assertTrue(f.acquire("UPLOAD_ARTIFACT").isNull());action(current,"RETRY");current=f.acquire("UPLOAD_ARTIFACT");assertEquals(upload.path("operationId"),current.path("operationId"));for(String field:List.of("payload","destination","storageRevision","accountRef"))assertEquals(upload.path(field),current.path(field));
        }
        fail(current,"RESULT_UNKNOWN","AUTH_RCLONE_FAILED",array(List.of()),array(List.of()));assertEquals(List.of("RECONCILE"),strings(task(current).path("allowedActions")));assertThrows(Failure.class,()->action(upload,"RETRY"));
    }
    @ParameterizedTest @ValueSource(strings={"EFFECTS","PENDING_EFFECTS","MISSING_EFFECTS","NULL_EFFECTS","RESULT_UNKNOWN","RUNNER_INTERRUPTED","CONTROL_PERSIST_FAILED","TOOL_STOP_UNCONFIRMED","STATE_UNKNOWN","NETWORK_INTERRUPTED","TOOL_OUTPUT_UNCONFIRMED"}) void uncertainUploadFailureNeverAutomaticallyOrExplicitlyRetries(String reason) {
        JsonNode upload=upload();String code=Set.of("EFFECTS","PENDING_EFFECTS","MISSING_EFFECTS","NULL_EFFECTS").contains(reason)?"AUTH_RCLONE_FAILED":reason;JsonNode completed=reason.equals("EFFECTS")?MAPPER.valueToTree(List.of("possible-object")):reason.equals("NULL_EFFECTS")?null:array(List.of());
        JsonNode pending=reason.equals("PENDING_EFFECTS")?MAPPER.valueToTree(List.of("unconfirmed-request")):array(List.of());
        ObjectNode request=f.result(upload,null),error=object("code",code,"completedEffects",completed,"pendingEffects",pending);if(reason.equals("MISSING_EFFECTS"))error.remove("completedEffects");request.put("status","RETRYABLE_ERROR");request.set("error",error);f.store.tx(()->f.app.queue.result(text(upload,"operationId"),request,false));
        assertEquals("NEEDS_ACTION",text(task(upload),"status"));assertEquals(List.of("RECONCILE"),strings(task(upload).path("allowedActions")));f.clock.now=f.clock.now.plusSeconds(601);assertTrue(f.acquire("UPLOAD_ARTIFACT").isNull());assertThrows(Failure.class,()->action(upload,"RETRY"));
    }
    @Test void readOnlyMetadataMayRetryItsOriginalIntentButAuthorizationAndWritesRemainBlocked() {
        f.store.tx(()->f.app.queue.enqueue("FETCH_BIL_METADATA","BILIBILI","metadata-read",null,"frozen",object("externalId","BVFixture")));JsonNode metadata=f.acquire("FETCH_BIL_METADATA");fail(metadata,"NEEDS_ACTION","AUTH_REQUIRED",array(List.of()),array(List.of()));assertTrue(strings(task(metadata).path("allowedActions")).contains("RETRY"));action(metadata,"RETRY");JsonNode retry=f.acquire("FETCH_BIL_METADATA");assertEquals(metadata.path("operationId"),retry.path("operationId"));assertEquals(metadata.path("payload"),retry.path("payload"));assertEquals(2,retry.path("attempt").asInt());
        ObjectNode session=f.store.tx(()->f.app.auth.begin(object("platform","BILIBILI")));JsonNode auth=f.acquire("AUTH_STEP");fail(auth,"NEEDS_ACTION","AUTH_REQUIRED",array(List.of()),array(List.of()));assertFalse(strings(task(auth).path("allowedActions")).contains("RETRY"));assertEquals("NEEDS_ACTION",text(f.store.tx(()->f.app.auth.session(text(session,"authSessionId"))),"state"));
    }
}
