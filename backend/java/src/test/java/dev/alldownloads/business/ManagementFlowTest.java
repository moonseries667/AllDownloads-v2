package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import java.nio.file.Path;
import java.util.*;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class ManagementFlowTest {
    @TempDir Path temp;
    BusinessFlowTest f;
    @BeforeEach void start() throws Exception {f=new BusinessFlowTest();f.temp=temp;f.open();}
    @AfterEach void close() throws Exception {f.close();}
    ObjectNode drive(String id) {return object("accountId","drive-account","folderId",id,"revision","v1","name","同名目录","path","根/同名目录");}
    void configureDrive() {f.store.tx(()->{f.store.put("settings","global",object("revision","1","archiveDestination",drive("folder-1")));return null;});}
    void twoParts() {
        ObjectNode content=(ObjectNode)f.examples.path("content").deepCopy(),second=(ObjectNode)content.path("mediaUnits").get(0).deepCopy();
        second.put("id","unit-second");second.put("index",2);second.put("title","第二部分");second.put("externalId","cid-second");
        for(JsonNode t:second.path("tracks"))((ObjectNode)t).put("id",text(t,"id")+"-second");
        content.put("kind","MULTIPART");content.set("mediaUnits",array(List.of(content.path("mediaUnits").get(0),second)));f.seed(content);
    }
    ObjectNode submitAll() {return f.submit(f.preview(f.request((String[])null)));}
    ObjectNode project(JsonNode submitted) {return f.store.tx(()->f.app.management.project(submitted.path("projectIds").get(0).asText()));}
    ObjectNode cancel(ObjectNode p,JsonNode child) {return f.store.tx(()->f.app.management.cancel(text(p,"id"),child==null?null:text(child,"id"),text(child==null?p:child,"revision")));}
    long uploads() {return f.store.tx(()->f.store.all("archive").size());}
    @Test void partialCompletionDoesNotQueueArchiveAndReplayQueuesExactlyOneFrozenUpload() throws Exception {
        configureDrive();twoParts();ObjectNode submitted=submitAll(),p=project(submitted);assertEquals(2,p.path("items").size());
        JsonNode first=f.acquire("DOWNLOAD_MEDIA"),second=f.acquire("DOWNLOAD_MEDIA");f.complete(first,f.fileFacts(first));assertEquals(0,uploads());
        f.store.tx(()->{f.store.put("settings","global",object("revision","2","archiveDestination",drive("folder-new")));return null;});
        ObjectNode last=f.complete(second,f.fileFacts(second));assertEquals(1,uploads());
        f.store.close();f.start();assertEquals(1,uploads());f.store.tx(()->f.app.queue.result(text(second,"operationId"),last,false));assertEquals(1,uploads());
        JsonNode archive=f.store.tx(()->f.app.management.archives(Map.of())).path("items").get(0);assertEquals("folder-1",text(archive.path("target"),"folderId"));assertEquals(6,archive.path("files").size());
        assertEquals("SUCCEEDED",text(project(submitted),"status"));
    }
    @Test void runningChildCancelWaitsForReceiptPreservesSiblingAndOriginalDenominatorAcrossRestart() throws Exception {
        configureDrive();twoParts();ObjectNode submitted=submitAll();JsonNode first=f.acquire("DOWNLOAD_MEDIA"),second=f.acquire("DOWNLOAD_MEDIA");ObjectNode p=project(submitted);
        JsonNode victim=list(p.path("items")).stream().filter(c->strings(c.path("taskIds")).contains(text(first,"taskId"))).findFirst().orElseThrow();
        ObjectNode accepted=cancel(p,victim);assertEquals("RUNNING",text(accepted,"status"));assertEquals("CANCEL",text(accepted,"desiredAction"));
        assertEquals(accepted,cancel(p,victim));assertEquals("CONTINUE",text(f.store.tx(()->f.store.get("task",text(second,"taskId"))),"desiredAction"));
        f.store.close();f.start();ObjectNode receipt=f.result(first,object("files",List.of(),"warnings",List.of()));receipt.put("status","CANCELLED");f.store.tx(()->f.app.queue.result(text(first,"operationId"),receipt,false));
        f.complete(second,f.fileFacts(second));p=project(submitted);assertEquals("PARTIAL",text(p,"status"));assertEquals(2,p.path("summary").path("total").asInt());assertEquals(1,p.path("summary").path("cancelled").asInt());assertEquals(0,uploads());assertTrue(f.acquire("DOWNLOAD_MEDIA").isNull());
    }
    @Test void cancellingOneConsumerDoesNotStopSharedTaskOrOtherInstance() {
        configureDrive();ObjectNode one=submitAll(),two=submitAll();assertNotEquals(one.path("orderId"),two.path("orderId"));
        JsonNode op=f.acquire("DOWNLOAD_MEDIA");ObjectNode p1=project(one),p2=project(two);
        cancel(p1,null);assertEquals("CONTINUE",text(f.store.tx(()->f.store.get("task",text(op,"taskId"))),"desiredAction"));assertEquals("CANCELLED",text(project(one),"status"));
        f.complete(op,f.fileFacts(op));assertEquals("SUCCEEDED",text(project(two),"status"));assertEquals(1,uploads());
        assertEquals(p2.path("id"),f.store.tx(()->f.app.management.archives(Map.of())).path("items").get(0).path("projectId"));
    }
    @Test void stableContentFilterSeparatesSameTitleAndKeepsAllBatchesWithBoundCursor() {
        ObjectNode first=submitAll();submitAll();ObjectNode other=(ObjectNode)f.examples.path("content").deepCopy();other.put("id","other-content");other.put("externalId","BV-other");
        for(JsonNode u:other.path("mediaUnits")){((ObjectNode)u).put("id",text(u,"id")+"-other");((ObjectNode)u).put("contentId","other-content");for(JsonNode t:u.path("tracks"))((ObjectNode)t).put("id",text(t,"id")+"-other");}f.seed(other);
        ObjectNode request=object("platform","BILIBILI","selection",object("mode","EXPLICIT","resource","BILIBILI_CONTENTS","ids",List.of("other-content")));f.submit(f.preview(request));
        ObjectNode page=f.store.tx(()->f.app.management.projects(Map.of("platform","BILIBILI","contentId","content-fixture","pageSize","1")));assertEquals(2,page.path("total").asInt());String cursor=text(page,"nextCursor");
        assertEquals(1,f.store.tx(()->f.app.management.projects(Map.of("platform","BILIBILI","contentId","content-fixture","pageSize","1","cursor",cursor))).path("items").size());
        BusinessFlowTest.assertCode("VERSION_CHANGED",()->f.store.tx(()->f.app.management.projects(Map.of("contentId","other-content","pageSize","1","cursor",cursor))));
        ObjectNode p=project(first);JsonNode op=f.acquire("DOWNLOAD_MEDIA");f.acquire("DOWNLOAD_MEDIA");BusinessFlowTest.assertCode("VERSION_CHANGED",()->f.store.tx(()->f.app.management.cancel(text(p,"id"),null,text(p,"revision"))));
        assertNotNull(op);
    }
    @Test void queuedCancelStopsAcquireAndCompletionRaceKeepsFilesWithoutArchive() {
        configureDrive();ObjectNode queued=submitAll();cancel(project(queued),null);assertTrue(f.acquire("DOWNLOAD_MEDIA").isNull());assertEquals("CANCELLED",text(project(queued),"status"));
        ObjectNode running=submitAll();JsonNode op=f.acquire("DOWNLOAD_MEDIA");cancel(project(running),null);f.complete(op,f.fileFacts(op));
        assertEquals("SUCCEEDED",text(project(running),"status"));assertEquals(0,uploads());assertFalse(f.store.tx(()->f.store.all("file")).isEmpty());
    }
    @Test void partialPublishedFilesAreKeptOnCancellationButNeverCompleteOriginalProject() {
        configureDrive();ObjectNode submitted=submitAll();JsonNode op=f.acquire("DOWNLOAD_MEDIA");cancel(project(submitted),null);
        ObjectNode facts=f.fileFacts(op);facts.set("files",array(List.of(facts.path("files").get(0))));ObjectNode receipt=f.result(op,facts);receipt.put("status","CANCELLED");f.store.tx(()->f.app.queue.result(text(op,"operationId"),receipt,false));
        ObjectNode p=project(submitted);assertEquals("CANCELLED",text(p,"status"));assertEquals(1,p.path("items").get(0).path("files").size());assertEquals(3,p.path("items").get(0).path("outputs").size());assertEquals(0,uploads());
    }
    @Test void missingFrozenDriveAndNonePolicyCannotBeAbsorbedByLaterSettings() {
        ObjectNode noDrive=submitAll();JsonNode first=f.acquire("DOWNLOAD_MEDIA");configureDrive();f.complete(first,f.fileFacts(first));assertEquals(0,uploads());
        ObjectNode local=f.submit(f.preview(f.custom(f.request("audio-fixture"),"local-only")));JsonNode second=f.acquire("DOWNLOAD_MEDIA");f.complete(second,f.fileFacts(second));assertEquals(0,uploads());assertEquals("NONE",text(project(local),"archivePolicy"));assertTrue(project(noDrive).path("archiveTarget").isNull());
    }
    @Test void explicitNewBatchCanReusePublishedSubsetWithoutRevivingCancelledInstance() {
        configureDrive();ObjectNode original=submitAll();JsonNode op=f.acquire("DOWNLOAD_MEDIA");cancel(project(original),null);
        ObjectNode subset=f.fileFacts(op);subset.set("files",array(List.of(subset.path("files").get(0))));ObjectNode receipt=f.result(op,subset);receipt.put("status","CANCELLED");f.store.tx(()->f.app.queue.result(text(op,"operationId"),receipt,false));
        ObjectNode retry=submitAll();JsonNode remaining=f.acquire("DOWNLOAD_MEDIA");assertEquals(2,remaining.path("payload").path("outputs").size());f.complete(remaining,f.fileFacts(remaining));
        assertEquals("CANCELLED",text(project(original),"status"));assertEquals("SUCCEEDED",text(project(retry),"status"));assertEquals(1,uploads());
    }
    @Test void expiredLeaseCancellationKeepsUncertaintyUntilMatchingReceiptReconciles() {
        configureDrive();ObjectNode submitted=submitAll();JsonNode op=f.acquire("DOWNLOAD_MEDIA");f.clock.now=f.clock.now.plusSeconds(91);f.store.tx(()->{f.app.queue.expire();return null;});
        ObjectNode accepted=cancel(project(submitted),null);assertEquals("NEEDS_ACTION",text(accepted,"status"));assertEquals("CANCEL",text(accepted,"desiredAction"));assertTrue(f.acquire("DOWNLOAD_MEDIA").isNull());
        ObjectNode stopped=f.result(op,object("files",List.of()));stopped.put("status","CANCELLED");BusinessFlowTest.assertCode("RECONCILE_REQUIRED",()->f.store.tx(()->f.app.queue.result(text(op,"operationId"),stopped,false)));
        f.store.tx(()->f.app.queue.result(text(op,"operationId"),stopped,true));assertEquals("CANCELLED",text(project(submitted),"status"));assertEquals(0,uploads());
    }
    @Test void uploadVerificationRejectsBareSuccessAndRetriesNeverCreateDownload() {
        configureDrive();submitAll();JsonNode download=f.acquire("DOWNLOAD_MEDIA");f.complete(download,f.fileFacts(download));
        f.store.tx(()->f.app.queue.register(object("runnerId","runner","protocolVersion",1,"capabilities",List.of("UPLOAD_ARTIFACT"),"tools",object())));
        JsonNode upload=f.acquire("UPLOAD_ARTIFACT");BusinessFlowTest.assertCode("INVALID_INPUT",()->f.complete(upload,object("verified",true)));
        ObjectNode failure=f.result(upload,null);failure.put("status","RETRYABLE_ERROR");failure.set("error",object("code","NETWORK_TEMPORARY","completedEffects",List.of(),"pendingEffects",List.of()));f.store.tx(()->f.app.queue.result(text(upload,"operationId"),failure,false));
        f.clock.now=f.clock.now.plusSeconds(31);JsonNode retry=f.acquire("UPLOAD_ARTIFACT");assertEquals(upload.path("operationId"),retry.path("operationId"));assertEquals(upload.path("payload"),retry.path("payload"));
        List<JsonNode> remote=new ArrayList<>();for(JsonNode file:retry.path("payload").path("files")) {ObjectNode r=(ObjectNode)file.deepCopy();String path=text(file,"relativePath");r.put("remoteId",id());r.put("remoteName",path.substring(path.lastIndexOf('/')+1));remote.add(r);}
        f.complete(retry,object("projectId",retry.path("payload").path("projectId"),"archiveTarget",retry.path("payload").path("archiveTarget"),"files",remote,"verified",true));
        assertEquals("SUCCEEDED",text(f.store.tx(()->f.app.management.archives(Map.of())).path("items").get(0),"status"));assertEquals(1,f.store.tx(()->f.store.rows("SELECT id FROM operations WHERE type='DOWNLOAD_MEDIA'").size()));
    }
}
