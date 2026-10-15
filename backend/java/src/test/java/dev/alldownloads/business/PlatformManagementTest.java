package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import java.nio.file.Path;
import java.util.*;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class PlatformManagementTest {
    @TempDir Path temp;
    FeedbackFlowTest f;
    @BeforeEach void start() throws Exception {f=new FeedbackFlowTest();f.temp=temp;f.open();f.store.tx(()->{f.store.put("settings","global",object("revision","1","archiveDestination",object("accountId","drive","folderId","archive","revision","1","name","归档","path","归档")));return null;});}
    @AfterEach void close() throws Exception {f.close();}
    List<JsonNode> projects(String platform) {return list(f.store.tx(()->f.app.management.projects(Map.of("platform",platform))).path("items"));}
    ObjectNode cancel(JsonNode project,JsonNode item) {return f.store.tx(()->f.app.management.cancel(text(project,"id"),item==null?null:text(item,"id"),text(item==null?project:item,"revision")));}
    @Test void telegramPhotoCancelDoesNotCancelVideoOrReappearOnIncrementalScanAndExplicitRetryCreatesInstance() throws Exception {
        JsonNode a=f.savedAlbum(),b=f.acquire("DOWNLOAD_MEDIA");JsonNode photo=text(a.path("payload").path("attachments").get(0),"mediaType").equals("PHOTO")?a:b,video=photo==a?b:a;
        assertEquals(1,a.path("payload").path("attachments").size());assertEquals(1,b.path("payload").path("attachments").size());assertEquals(a.path("payload").path("messageIds"),b.path("payload").path("messageIds"));
        JsonNode p=projects("TELEGRAM").get(0);assertEquals(2,p.path("items").size());JsonNode item=list(p.path("items")).stream().filter(c->strings(c.path("taskIds")).contains(text(photo,"taskId"))).findFirst().orElseThrow();
        assertEquals("RUNNING",text(cancel(p,item),"status"));assertEquals("CONTINUE",text(f.store.tx(()->f.store.get("task",text(video,"taskId"))),"desiredAction"));
        f.store.tx(()->f.app.queue.result(text(photo,"operationId"),f.report(photo,"CANCELLED",object("files",List.of()),null),false));f.success(video,object("files",f.evidence(video),"warnings",List.of()));
        String original=text(p,"id");assertEquals("PARTIAL",text(projects("TELEGRAM").get(0),"status"));assertEquals(0,f.store.tx(()->f.store.all("archive").size()));
        f.store.close();f.start();f.store.tx(()->f.app.feedback.savedSync(object("accountId","tel-test")));JsonNode scan=f.acquire("SCAN_TEL_SAVED");f.success(scan,f.examples.path("telegramPages").get(1));assertTrue(f.acquire("DOWNLOAD_MEDIA").isNull());
        f.store.tx(()->f.app.feedback.retryIncomplete(object("accountId","tel-test")));JsonNode retry=f.acquire("DOWNLOAD_MEDIA");assertNotEquals(photo.path("operationId"),retry.path("operationId"));assertEquals(photo.path("payload"),retry.path("payload"));f.success(retry,object("files",f.evidence(retry),"warnings",List.of()));
        assertEquals(2,projects("TELEGRAM").size());assertEquals("PARTIAL",text(f.store.tx(()->f.app.management.project(original)),"status"));assertEquals(1,f.store.tx(()->f.store.all("archive").size()));
        assertEquals("COMPLETE",f.store.tx(()->f.app.feedback.telegram.messages("tel-test")).get(0).path("downloadSummary").path("state").asText());
    }
    @Test void quarkBatchSplitsSharesAndOnlyIndependentCompleteShareEntersArchive() {
        String first=f.seedShare();String second=f.store.tx(()->list(f.app.feedback.quark.shares()).stream().map(s->text(s,"id")).filter(id->!id.equals(first)).findFirst().orElseThrow());
        f.store.tx(()->{f.app.feedback.quark.acceptNodes(first,array(List.of(f.node(first,"one","1"),f.node(first,"two","1"))),true);f.app.feedback.quark.acceptNodes(second,array(List.of(f.node(second,"three","1"))),true);return null;});
        String preview=f.store.tx(()->f.app.downloads.preview(object("platform","QUARK","selection",object("mode","EXPLICIT","resource","QUARK_NODES","ids",List.of("one","two","three"))))).path("previewId").asText();ObjectNode batch=f.store.tx(()->f.app.downloads.submit(object("previewId",preview)));assertEquals(2,batch.path("projectIds").size());
        JsonNode p=projects("QUARK").stream().filter(v->text(v,"contentId").equals(first)).findFirst().orElseThrow();JsonNode cancelled=p.path("items").get(0);cancel(p,cancelled);
        for(int i=0;i<2;i++){JsonNode op=f.acquire("DOWNLOAD_MEDIA");assertFalse(op.isNull());assertNotEquals(cancelled.path("nodeId"),op.path("payload").path("nodes").get(0).path("id"));f.success(op,object("files",f.evidence(op),"warnings",List.of()));}
        assertTrue(f.acquire("DOWNLOAD_MEDIA").isNull());assertEquals("PARTIAL",text(f.store.tx(()->f.app.management.project(text(p,"id"))),"status"));
        JsonNode archive=f.store.tx(()->f.app.management.archives(Map.of())).path("items");assertEquals(1,archive.size());assertEquals(second,text(f.store.tx(()->f.app.management.project(text(archive.get(0),"projectId"))),"contentId"));
    }
}
