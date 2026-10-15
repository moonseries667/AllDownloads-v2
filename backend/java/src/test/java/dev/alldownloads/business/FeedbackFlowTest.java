package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.*;
import dev.alldownloads.feedback2.DomainSql;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import java.nio.file.Path;
import java.time.*;
import java.util.*;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class FeedbackFlowTest {
    @TempDir Path temp;
    BusinessStore store;BusinessApplication app;JsonNode examples;MutableClock clock=new MutableClock();
    @BeforeEach void open() throws Exception {examples=MAPPER.readTree(Path.of("../../contracts/feedback2.examples.json").toFile());start();JsonNode target=acquire("VALIDATE_TARGET");if(!target.isNull())success(target,object("targetId","default","rootId","default-root","canonicalPath",temp.resolve("default").toString(),"writable",true));}
    void start() throws Exception {store=new BusinessStore(temp.resolve("control.db"),clock);app=new BusinessApplication(store,temp.resolve("default").toString());store.tx(()->app.queue.register(object("runnerId","fixture","protocolVersion",1,"capabilities",List.of("VALIDATE_TARGET","SCAN_TEL_SAVED","LIST_QUARK_TREE","TRANSFER_QUARK_ITEMS","DOWNLOAD_MEDIA"),"tools",object())));}
    @AfterEach void close() throws Exception {if(store!=null)store.close();}
    JsonNode acquire(String type){return store.tx(()->app.queue.acquire(object("runnerId","fixture","capabilities",List.of(type))).path("operation"));}
    ObjectNode report(JsonNode op,String status,JsonNode result,JsonNode error){return object("eventId",id(),"runnerId",op.path("runnerId"),"leaseId",op.path("leaseId"),"attempt",op.path("attempt"),"fencingToken",op.path("fencingToken"),"status",status,"result",result,"error",error);}
    ObjectNode success(JsonNode op,JsonNode result){return store.tx(()->app.queue.result(text(op,"operationId"),report(op,"SUCCEEDED",result,null),false));}
    ArrayNode evidence(JsonNode op){ArrayNode files=MAPPER.createArrayNode();for(var output:op.path("payload").path("outputs"))files.add(object("outputId",output.path("id"),"relativePath",output.path("relativePath"),"sizeBytes",output.hasNonNull("sizeBytes")?output.path("sizeBytes").asText():"1","sha256","a".repeat(64),"mediaKinds",List.of(text(output,"kind")),"durationSeconds",null,"verified",true));return files;}
    JsonNode savedAlbum(){store.tx(()->app.feedback.savedSync(object("accountId","tel-test")));JsonNode scan=acquire("SCAN_TEL_SAVED");success(scan,examples.path("telegramPages").get(0));JsonNode next=acquire("SCAN_TEL_SAVED");success(next,examples.path("telegramPages").get(1));return acquire("DOWNLOAD_MEDIA");}
    @Test void groupedSavedScanUsesSharedQueueAndStableCursorWithoutManualOrders(){
        assertTrue(store.tx(()->app.feedback.telegram.messages(null)).isEmpty());JsonNode download=savedAlbum();assertEquals("TELEGRAM",text(download,"platform"));assertEquals("tel-test",text(download,"accountRef"));assertEquals(4,download.path("payload").path("outputs").size());assertEquals(0,store.tx(()->store.all("order").size()));
        success(download,object("files",evidence(download),"warnings",List.of()));assertEquals("VERIFIED",store.tx(()->app.feedback.telegram.messages("tel-test")).get(0).path("attachments").get(0).path("downloadState").asText());
        assertEquals(1,store.tx(()->store.rows("SELECT id FROM operations WHERE type='UPLOAD_ARTIFACT'").size()));
        store.tx(()->app.feedback.savedSync(object("accountId","tel-test")));JsonNode again=acquire("SCAN_TEL_SAVED");assertEquals("2",again.path("payload").path("afterMessageId").asText());success(again,object("accountId","tel-test","messages",List.of(),"complete",true,"scannedThroughMessageId","2","continuation",null));assertTrue(acquire("DOWNLOAD_MEDIA").isNull());
    }
    @Test void partialPhotoFailurePersistsNeedsAttentionAndExplicitRetryOnlyCompletesMissingFiles() throws Exception {
        JsonNode download=savedAlbum();ArrayNode all=evidence(download),partial=MAPPER.createArrayNode().add(all.get(0)).add(all.get(1));
        ObjectNode failure=report(download,"RETRYABLE_ERROR",object("files",partial,"warnings",List.of()),object("code","NETWORK_TEMPORARY","message","do not expose private transport","completedEffects",List.of(all.get(0).path("outputId").asText(),all.get(1).path("outputId").asText()),"pendingEffects",List.of(all.get(2).path("outputId").asText()),"retryAfterSeconds",null));
        ObjectNode ack=store.tx(()->app.queue.result(text(download,"operationId"),failure,false));assertEquals("NEEDS_ACTION",text(ack,"status"));String issue=store.tx(()->app.feedback.issues.list(false)).get(0).path("id").asText();store.tx(()->app.feedback.issues.acknowledge(issue));
        assertEquals(ack,store.tx(()->app.queue.result(text(download,"operationId"),failure,false)));assertEquals(1,store.tx(()->app.feedback.issues.list(false)).size());store.close();store=null;start();
        assertTrue(store.tx(()->app.feedback.issues.get(issue)).path("read").asBoolean());assertFalse(store.tx(()->app.feedback.issues.get(issue)).path("resolved").asBoolean());assertEquals(1,store.tx(()->app.feedback.telegram.messages(null)).get(0).path("downloadSummary").path("completed").asInt());
        store.tx(()->app.feedback.retryIncomplete(object("accountId","tel-test")));JsonNode retry=acquire("DOWNLOAD_MEDIA");assertEquals(text(download,"operationId"),text(retry,"operationId"));assertEquals(download.path("payload"),retry.path("payload"));success(retry,object("files",all,"warnings",List.of()));assertTrue(store.tx(()->app.feedback.issues.get(issue)).path("resolved").asBoolean());
    }
    @Test void watcherOnlyRunsAfterExplicitEnableAndMergesActiveSync(){
        JsonNode download=savedAlbum();ObjectNode watcher=store.tx(()->app.feedback.telegram.watcher("tel-test"));assertFalse(watcher.path("enabled").asBoolean());
        store.tx(()->app.feedback.telegram.setWatcher("tel-test",true,text(watcher,"revision")));clock.instant=clock.instant.plusSeconds(61);app.feedback.tick();String first=store.tx(()->app.feedback.telegram.activeTask("tel-test"));assertNotNull(first);app.feedback.tick();assertEquals(first,store.tx(()->app.feedback.telegram.activeTask("tel-test")));assertEquals(3,store.tx(()->store.rows("SELECT id FROM operations WHERE type='SCAN_TEL_SAVED'").size()));assertFalse(download.isNull());
    }
    @Test void previousKnownFailureCannotAuthorizeRetryAfterASecondLeaseBecomesUnknown(){
        JsonNode op=savedAlbum();ObjectNode failure=report(op,"NEEDS_ACTION",object("files",List.of()),object("code","NETWORK_TEMPORARY","completedEffects",List.of(),"pendingEffects",List.of()));store.tx(()->app.queue.result(text(op,"operationId"),failure,false));store.tx(()->app.feedback.retryIncomplete(object("accountId","tel-test")));assertFalse(acquire("DOWNLOAD_MEDIA").isNull());clock.instant=clock.instant.plusSeconds(91);store.tx(()->{app.queue.expire();return null;});
        assertEquals("RECONCILE_REQUIRED",assertThrows(Failure.class,()->store.tx(()->app.feedback.retryIncomplete(object("accountId","tel-test")))).code);
    }
    @Test void explicitlyCompletingACancelledSavedDownloadRetainsOperationAndFrozenTarget(){
        JsonNode op=savedAlbum();String task=text(op,"taskId");store.tx(()->app.queue.control(task,text(store.get("task",task),"revision"),"CANCEL"));store.tx(()->app.queue.result(text(op,"operationId"),report(op,"CANCELLED",object("files",List.of()),null),false));
        store.tx(()->app.feedback.retryIncomplete(object("accountId","tel-test")));JsonNode retry=acquire("DOWNLOAD_MEDIA");assertEquals(op.path("operationId"),retry.path("operationId"));assertEquals(op.path("destination"),retry.path("destination"));success(retry,object("files",evidence(retry),"warnings",List.of()));
    }
    String seedShare(){store.tx(()->{app.feedback.quark.acceptTree((ObjectNode)examples.path("quarkRoot"));return null;});ObjectNode result=store.tx(()->app.feedback.addShares(examples.path("quarkSharedTarget")));return result.path("items").get(0).path("shareId").asText();}
    ObjectNode node(String share,String id,String version){return object("id",id,"shareId",share,"parentId",null,"externalId",id,"version",version,"name",id+".mp4","kind","FILE","relativePath","课程/"+id+".mp4","sizeBytes","7","modifiedAt",null,"availability","AVAILABLE","availabilityReason",null,"downloadSummary",object("state","NOT_REQUESTED"));}
    @Test void quarkCustomTargetReusesOrdersAndNeverArchivesAfterRestartReplayOrNewNodes() throws Exception {
        String share=seedShare();assertEquals(0,store.tx(()->store.all("order").size()));assertTrue(acquire("DOWNLOAD_MEDIA").isNull());
        ObjectNode file=node(share,"file","v1");store.tx(()->{app.feedback.quark.acceptNodes(share,array(List.of(file)),true);return null;});
        String target=store.tx(()->app.downloads.validateTarget(object("path",temp.resolve("custom").toString()))).path("targetId").asText();JsonNode validate=acquire("VALIDATE_TARGET");success(validate,object("targetId",target,"rootId","custom-root","canonicalPath",temp.resolve("custom").toString(),"writable",true));
        String preview=store.tx(()->app.downloads.preview(object("platform","QUARK","selection",object("mode","EXPLICIT","resource","QUARK_NODES","ids",List.of("file")),"target",object("id",target,"revision","1")))).path("previewId").asText();JsonNode shown=store.tx(()->app.downloads.getPreview(preview));assertEquals("NONE",text(shown,"archivePolicy"));assertEquals("7",shown.path("items").get(0).path("sizeBytes").asText());assertEquals("CUSTOM_TARGET_NO_ARCHIVE",shown.path("items").get(0).path("reasonCode").asText());
        store.tx(()->{app.feedback.quark.acceptNodes(share,array(List.of(file,node(share,"new","1"))),true);return null;});ObjectNode order=store.tx(()->app.downloads.submit(object("previewId",preview)));JsonNode download=acquire("DOWNLOAD_MEDIA");assertEquals("QUARK",text(download,"platform"));assertEquals("quark-test",text(download,"accountRef"));assertEquals(1,download.path("payload").path("nodes").size());assertEquals("Quark/12345678/课程/file.mp4",download.path("payload").path("outputs").get(0).path("relativePath").asText());
        ObjectNode report=report(download,"SUCCEEDED",object("files",evidence(download),"warnings",List.of()),null);ObjectNode ack=store.tx(()->app.queue.result(text(download,"operationId"),report,false));store.close();store=null;start();assertEquals(ack,store.tx(()->app.queue.result(text(download,"operationId"),report,false)));assertEquals(0,store.tx(()->store.rows("SELECT id FROM operations WHERE type='UPLOAD_ARTIFACT'").size()));assertEquals("SUCCEEDED",store.tx(()->app.downloads.order(text(order,"orderId"))).path("status").asText());assertEquals("folder-a",store.tx(()->app.feedback.quark.share(share)).path("targetFolderId").asText());
    }
    @Test void quarkSelectedVersionAndTargetChangesRejectOldPreview(){
        String share=seedShare();ObjectNode file=node(share,"file","1");store.tx(()->{app.feedback.quark.acceptNodes(share,array(List.of(file)),true);return null;});String preview=store.tx(()->app.downloads.preview(object("platform","QUARK","selection",object("mode","EXPLICIT","resource","QUARK_NODES","ids",List.of("file"))))).path("previewId").asText();
        store.tx(()->{app.feedback.quark.acceptNodes(share,array(List.of(file.deepCopy().put("version","2"))),true);return null;});assertEquals("VERSION_CHANGED",assertThrows(DomainSql.DomainError.class,()->store.tx(()->app.downloads.submit(object("previewId",preview)))).code);assertEquals(0,store.tx(()->store.all("order").size()));
        assertEquals("INVALID_INPUT",assertThrows(Failure.class,()->store.tx(()->app.downloads.preview(object("platform","TELEGRAM","selection",object("mode","EXPLICIT","resource","TELEGRAM_ATTACHMENTS","ids",List.of("file")))))).code);
    }
    static final class MutableClock extends Clock {Instant instant=Instant.parse("2026-10-15T10:00:00Z");public ZoneId getZone(){return ZoneOffset.UTC;}public Clock withZone(ZoneId zone){return this;}public Instant instant(){return instant;}}
}
