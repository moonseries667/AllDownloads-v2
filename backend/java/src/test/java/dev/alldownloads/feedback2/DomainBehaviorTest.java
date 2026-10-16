package dev.alldownloads.feedback2;

import com.fasterxml.jackson.databind.*;
import com.fasterxml.jackson.databind.node.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import java.nio.file.Path;
import java.sql.*;
import java.time.*;
import java.util.*;
import static org.junit.jupiter.api.Assertions.*;

class DomainBehaviorTest {
    @TempDir Path temp;
    final ObjectMapper json=new ObjectMapper();
    Connection db; TelegramLibrary tel; QuarkCatalog quark; IssueRepository issues;
    final Clock clock=Clock.fixed(Instant.parse("2026-10-15T10:00:00Z"),ZoneOffset.UTC);
    JsonNode examples;
    @BeforeEach void open() throws Exception { examples=json.readTree(Path.of("../../contracts/feedback2.examples.json").toFile()); connect(); }
    void connect() throws Exception {
        db=DriverManager.getConnection("jdbc:sqlite:"+temp.resolve("domain.db"));db.setAutoCommit(false);
        db.createStatement().execute("CREATE TABLE IF NOT EXISTS objects(kind TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(kind,id))");
        issues=new IssueRepository(db,json,clock);tel=new TelegramLibrary(db,json,clock,issues);quark=new QuarkCatalog(db,json,clock);
        issues.initialize();tel.initialize();quark.initialize();
        db.createStatement().execute("CREATE TABLE IF NOT EXISTS test_queue(id TEXT PRIMARY KEY,type TEXT,resource TEXT UNIQUE,payload TEXT)");db.commit();
    }
    @AfterEach void close() throws Exception {if(db!=null)db.close();}
    String enqueue(String type,String platform,String account,String resource,ObjectNode payload) throws SQLException {
        String id="task-"+resource;
        try(var s=db.prepareStatement("INSERT OR IGNORE INTO test_queue VALUES(?,?,?,?)")){s.setString(1,id);s.setString(2,type);s.setString(3,resource);s.setString(4,payload.toString());s.executeUpdate();}return id;
    }
    List<ObjectNode> downloads() throws Exception {List<ObjectNode> out=new ArrayList<>();try(var s=db.createStatement();var r=s.executeQuery("SELECT payload FROM test_queue WHERE type='DOWNLOAD_MEDIA'")){while(r.next())out.add((ObjectNode)json.readTree(r.getString(1)));}return out;}
    ObjectNode object(String value) throws Exception {return (ObjectNode)json.readTree(value);}
    ObjectNode attachment(String type) {return json.createObjectNode().put("id",type).put("externalId",type).put("name",type+".bin").put("mediaType",type).putNull("mimeType").putNull("sizeBytes");}
    ObjectNode message(String id,String... types) {
        ObjectNode m=json.createObjectNode().put("id",id).put("externalId",id).put("sentAt","2026-10-14T12:00:00Z").put("text","text "+id).putNull("groupId").put("groupComplete",true);ArrayNode a=m.putArray("attachments");for(String type:types)a.add(attachment(type));m.putArray("sourceLinks");m.putArray("authorTags");m.putNull("channel");return m;
    }
    ObjectNode page(String through,ObjectNode... messages) {ObjectNode p=json.createObjectNode().put("accountId","tel-test").put("complete",true).put("scannedThroughMessageId",through).putNull("continuation");ArrayNode a=p.putArray("messages");for(var m:messages)a.add(m);return p;}
    @Test void startupAndWatcherReadsNeverQueueAndInitialScanIsRequired() throws Exception {
        assertEquals("0",tel.watcher("tel-test").path("scanState").path("cursorMessageId").asText());assertTrue(downloads().isEmpty());
        assertEquals("INITIAL_SCAN_REQUIRED",assertThrows(DomainSql.DomainError.class,()->tel.setWatcher("tel-test",true,"0")).code);
    }
    @Test void exactEligibilityMatrixIncludesEveryMixedAttachmentAndSidecar() throws Exception {
        tel.beginScan("tel-test","scan");
        tel.acceptPage("scan","page",page("7",message("1"),message("2","PHOTO"),message("3","DOCUMENT"),message("4","PHOTO","DOCUMENT"),message("5","AUDIO"),message("6","VIDEO","PHOTO","DOCUMENT"),message("7","UNKNOWN")),this::enqueue);
        assertEquals(2,tel.messages("tel-test").size());assertEquals("7",tel.watcher("tel-test").path("scanState").path("cursorMessageId").asText());assertTrue(tel.watcher("tel-test").path("scanState").path("initialScanCompleted").asBoolean());
        assertEquals(8,downloads().stream().mapToInt(n->n.path("outputs").size()).sum());assertEquals(4,downloads().stream().mapToInt(n->n.path("attachments").size()).sum());
        for(var m:tel.messages("tel-test"))for(var a:m.path("attachments"))assertEquals(a.path("relativePath").asText()+".metadata.json",a.path("metadataRelativePath").asText());
        db.commit();db.close();connect();assertEquals("7",tel.beginScan("tel-test","scan2").path("afterMessageId").asText());tel.acceptPage("scan2","empty",page("7"),this::enqueue);assertEquals(2,downloads().size());
    }
    @Test void mimeFactsQualifyButFilenameAndTextDoNot() throws Exception {
        ObjectNode actual=message("1","UNKNOWN");((ObjectNode)actual.path("attachments").get(0)).put("mimeType","audio/ogg");
        ObjectNode fake=message("2","DOCUMENT");((ObjectNode)fake.path("attachments").get(0)).put("name","a.mp4").put("mimeType","application/pdf");fake.put("text","video audio");
        tel.beginScan("tel-test","scan");tel.acceptPage("scan","p",page("2",actual,fake),this::enqueue);assertEquals(1,downloads().size());assertEquals("AUDIO",downloads().getFirst().path("attachments").get(0).path("mediaType").asText());
    }
    @Test void groupAcrossPagesAndRestartKeepsPhotoAndStableIdentity() throws Exception {
        tel.beginScan("tel-test","scan");ObjectNode first=(ObjectNode)examples.path("telegramPages").get(0);
        tel.acceptPage("scan","page1",first,this::enqueue);assertEquals("0",tel.watcher("tel-test").path("scanState").path("cursorMessageId").asText());assertTrue(downloads().isEmpty());db.commit();db.close();connect();
        String next=tel.activeTask("tel-test");ObjectNode second=(ObjectNode)examples.path("telegramPages").get(1);tel.acceptPage(next,"page2",second,this::enqueue);tel.acceptPage(next,"page2",second,this::enqueue);
        assertEquals(1,downloads().size());assertEquals(2,downloads().getFirst().path("attachments").size());assertEquals(4,downloads().getFirst().path("outputs").size());String id=tel.messages("tel-test").get(0).path("id").asText();
        tel.beginScan("tel-test","again");tel.acceptPage("again","page3",second,this::enqueue);assertEquals(id,tel.messages("tel-test").get(0).path("id").asText());assertEquals(1,downloads().size());
    }
    @Test void unfinishedGroupAtIncrementBoundaryIsRetainedAndNotBusyRescanned() throws Exception {
        ObjectNode p=((ObjectNode)examples.path("telegramPages").get(0)).deepCopy().put("complete",true).putNull("continuation");tel.beginScan("tel-test","scan");tel.acceptPage("scan","p",p,this::enqueue);
        assertNull(tel.activeTask("tel-test"));assertFalse(tel.watcher("tel-test").path("scanState").path("initialScanCompleted").asBoolean());assertEquals(1,issues.list(false).size());
        tel.beginScan("tel-test","next");tel.acceptPage("next","p2",(ObjectNode)examples.path("telegramPages").get(1),this::enqueue);assertEquals(1,downloads().size());assertTrue(issues.list(false).isEmpty());
    }
    @Test void cursorAndFactsRollbackWhenOperationCreationFails() throws Exception {
        tel.beginScan("tel-test","scan");db.commit();
        assertThrows(SQLException.class,()->tel.acceptPage("scan","p",page("1",message("1","VIDEO")),(a,b,c,d,e)->{throw new SQLException("injected transaction failure");}));db.rollback();
        assertEquals("0",tel.watcher("tel-test").path("scanState").path("cursorMessageId").asText());assertTrue(tel.messages("tel-test").isEmpty());tel.acceptPage("scan","p",page("1",message("1","VIDEO")),this::enqueue);assertEquals(1,downloads().size());
    }
    @Test void partialDownloadNeverMarksPhotosVerifiedAndRetryKeepsFrozenPlan() throws Exception {
        tel.beginScan("tel-test","scan");tel.acceptPage("scan","p",page("1",message("1","VIDEO","PHOTO")),this::enqueue);
        String task=tel.incompleteTasks("tel-test",null).getFirst();String frozen=downloads().getFirst().toString();ArrayNode files=evidence(downloads().getFirst());ArrayNode partial=json.createArrayNode().add(files.get(0)).add(files.get(1));tel.downloadFacts(task,partial,false);db.commit();db.close();connect();
        assertEquals(List.of(task),tel.incompleteTasks("tel-test",null));assertEquals("NEEDS_ACTION",tel.messages("tel-test").get(0).path("attachments").get(1).path("downloadState").asText());assertEquals(1,tel.messages("tel-test").get(0).path("downloadSummary").path("completed").asInt());assertEquals(frozen,downloads().getFirst().toString());
        assertEquals("VERIFICATION_FAILED",assertThrows(DomainSql.DomainError.class,()->tel.downloadFacts(task,partial,true)).code);db.rollback();tel.downloadFacts(task,files,true);assertTrue(tel.incompleteTasks("tel-test",null).isEmpty());
    }
    ArrayNode evidence(ObjectNode payload) {ArrayNode files=json.createArrayNode();for(var o:payload.path("outputs"))files.add(json.createObjectNode().put("outputId",o.path("id").asText()).put("relativePath",o.path("relativePath").asText()).put("sizeBytes",o.hasNonNull("sizeBytes")?o.path("sizeBytes").asText():"1").put("sha256","a".repeat(64)).put("verified",true));return files;}
    @Test void attachmentNamesCannotCollideWithAnotherAttachmentsSidecar() throws Exception {
        ObjectNode m=message("1","VIDEO","DOCUMENT");((ObjectNode)m.path("attachments").get(0)).put("name","media.mp4");((ObjectNode)m.path("attachments").get(1)).put("name","media.mp4.metadata.json");tel.beginScan("tel-test","scan");tel.acceptPage("scan","p",page("1",m),this::enqueue);
        Set<String> paths=new HashSet<>();for(var o:downloads().getFirst().path("outputs"))assertTrue(paths.add(o.path("relativePath").asText().toLowerCase(Locale.ROOT)));assertEquals(4,paths.size());
    }
    @Test void notificationsHaveStableIdentityAndAcknowledgementCannotResolve() throws Exception {
        ObjectNode first=issues.record("task","resource","NETWORK_TEMPORARY","ERROR");String id=first.path("id").asText();issues.acknowledge(id);issues.record("task","resource","NETWORK_TEMPORARY","ERROR");db.commit();db.close();connect();
        assertEquals(1,issues.list(false).size());assertTrue(issues.get(id).path("read").asBoolean());assertFalse(issues.get(id).path("resolved").asBoolean());
        issues.record("task","resource","METADATA_WARNING","WARNING");issues.taskSucceeded("task");assertEquals(1,issues.list(false).size());assertEquals("WARNING",issues.list(false).get(0).path("severity").asText());assertEquals(2,issues.list(true).size());
    }
    void root() throws Exception {quark.acceptTree((ObjectNode)examples.path("quarkRoot"));}
    ObjectNode folder(String id,String parent,String name) {return json.createObjectNode().put("id",id).put("accountId","quark-test").put("parentId",parent).put("name",name).put("kind","FOLDER").put("revision","v1").put("selectable",true).put("availability","AVAILABLE").putNull("availabilityReason");}
    ObjectNode tree(String parent,ObjectNode...folders) {ObjectNode r=json.createObjectNode().put("accountId","quark-test").put("parentId",parent).put("complete",true);ArrayNode a=r.putArray("folders");for(var f:folders)a.add(f);r.putArray("nodes");return r;}
    @Test void folderRootPathsAndSameNamesUseNodeIdentity() throws Exception {
        root();assertEquals("",quark.folder("quark-test","0").path("path").asText());assertEquals("我的云盘",quark.folders("quark-test",null).path("parent").path("name").asText());
        quark.acceptTree(tree("folder-a",folder("child-1","folder-a","第一课"),folder("child-2","folder-a","第一课")));
        assertEquals("课程/第一课",quark.folder("quark-test","child-1").path("path").asText());assertEquals(2,quark.folders("quark-test","folder-a").path("items").size());
        quark.acceptTree(tree("child-1"));assertEquals("READY",quark.folders("quark-test","child-1").path("status").asText());assertTrue(quark.folders("quark-test","child-1").path("items").isEmpty());
    }
    @Test void transferTargetsFreezeBatchAndIndividualMappingWithoutDownloads() throws Exception {
        root();ObjectNode request=((ObjectNode)examples.path("quarkSharedTarget")).deepCopy();ObjectNode result=quark.addShares(request,this::enqueue);assertEquals(2,result.path("items").size());
        for(var item:result.path("items"))assertEquals("folder-a",quark.share(item.path("shareId").asText()).path("targetFolderId").asText());assertTrue(downloads().isEmpty());
        quark.acceptTree(tree("folder-a",folder("child","folder-a","课程")));ObjectNode individual=request.deepCopy();((ObjectNode)individual.path("entries").get(0)).put("url","https://pan.quark.cn/s/new12345678").put("targetFolderId","child").put("targetFolderRevision","v1");((ObjectNode)individual.path("entries").get(1)).put("url","https://pan.quark.cn/s/new87654321");
        ObjectNode created=quark.addShares(individual,this::enqueue);assertEquals("child",quark.share(created.path("items").get(0).path("shareId").asText()).path("targetFolderId").asText());assertEquals("folder-a",quark.share(created.path("items").get(1).path("shareId").asText()).path("targetFolderId").asText());assertTrue(downloads().isEmpty());
    }
    @Test void removedAndChangedTargetsNeverFallBackToRoot() throws Exception {
        root();ObjectNode request=((ObjectNode)examples.path("quarkSharedTarget")).deepCopy();((ObjectNode)request.path("entries").get(0)).put("targetFolderRevision","old");
        assertEquals("VERSION_CHANGED",quark.addShares(request,this::enqueue).path("items").get(0).path("error").path("code").asText());
        quark.acceptTree(tree("0"));assertEquals("TARGET_UNAVAILABLE",quark.addShares(request,this::enqueue).path("items").get(0).path("error").path("code").asText());assertTrue(downloads().isEmpty());
    }
    @Test void incompleteTreeCannotExpandAndLaterAdditionsDoNotMutateSnapshot() throws Exception {
        root();String share=quark.addShares((ObjectNode)examples.path("quarkSharedTarget"),this::enqueue).path("items").get(0).path("shareId").asText();
        ObjectNode folder=json.createObjectNode().put("id","dir").put("shareId",share).putNull("parentId").put("externalId","dir").put("version","v1").put("name","课程").put("kind","FOLDER").put("relativePath","课程").put("availability","AVAILABLE").putNull("sizeBytes");
        ObjectNode file=folder.deepCopy().put("id","file").put("externalId","file").put("parentId","dir").put("name","课.mp4").put("kind","FILE").put("relativePath","课程/课.mp4").put("sizeBytes","0");ArrayNode nodes=json.createArrayNode().add(folder).add(file);
        quark.acceptNodes(share,nodes,false);assertEquals("SNAPSHOT_INCOMPLETE",assertThrows(DomainSql.DomainError.class,()->quark.expand(Set.of("dir"),null,null)).code);quark.acceptNodes(share,nodes,true);ArrayNode frozen=quark.expand(Set.of("dir"),null,null);assertEquals(1,frozen.size());assertEquals("0",frozen.get(0).path("sizeBytes").asText());assertEquals("Quark/12345678/课程/课.mp4",frozen.get(0).path("plannedRelativePath").asText());
        nodes.add(file.deepCopy().put("id","new").put("externalId","new").put("name","new.mp4").put("relativePath","课程/new.mp4"));quark.acceptNodes(share,nodes,true);assertEquals(1,frozen.size());quark.validateFrozen(frozen);file.put("version","v2");quark.acceptNodes(share,nodes,true);assertThrows(DomainSql.DomainError.class,()->quark.validateFrozen(frozen));
    }
    @Test void providerShareRootOutsideContentNodesDoesNotRemoveUnrelatedFolders() throws Exception {
        root();quark.acceptTree(tree("0",folder("folder-a","0","课程"),folder("unrelated","0","其他")));
        ObjectNode req=((ObjectNode)examples.path("quarkSharedTarget")).deepCopy();for(var e:req.path("entries"))((ObjectNode)e).put("targetFolderRevision","v1");
        String share=quark.addShares(req,this::enqueue).path("items").get(0).path("shareId").asText();
        ObjectNode result=tree(null,folder("0",null,"我的云盘"),folder("folder-a","0","课程"),folder("managed","folder-a","转存目录")).put("shareId",share);
        ((ArrayNode)result.path("nodes")).add(json.createObjectNode().put("id","f").put("shareId",share).put("parentId","managed").put("externalId","f").put("version","1").put("name","音频.ogg").put("kind","FILE").put("relativePath","音频.ogg").put("availability","AVAILABLE").putNull("sizeBytes"));
        quark.acceptTree(result);assertTrue(quark.folder("quark-test","unrelated").path("selectable").asBoolean());assertEquals(1,quark.expand(Set.of("f"),null,null).size());assertEquals("Quark/12345678/音频.ogg",quark.expand(Set.of("f"),null,null).get(0).path("plannedRelativePath").asText());
    }
    @Test void cloudColonNameAndRelativePathStayRawWhileLocalPathIsWindowsSafe() throws Exception {
        root();ObjectNode request=((ObjectNode)examples.path("quarkSharedTarget")).deepCopy();((ArrayNode)request.path("entries")).remove(1);
        String share=quark.addShares(request,this::enqueue).path("items").get(0).path("shareId").asText();
        ObjectNode file=json.createObjectNode().put("id","colon-file").put("shareId",share).putNull("parentId").put("externalId","colon-file").put("version","v1").put("name","讲义:2026.mp4").put("kind","FILE").put("relativePath","课程/讲义:2026.mp4").put("sizeBytes","7").put("availability","AVAILABLE").putNull("availabilityReason");
        quark.acceptNodes(share,json.createArrayNode().add(file),true);
        JsonNode stored=quark.nodes(share).get(0);assertEquals("讲义:2026.mp4",stored.path("name").asText());assertEquals("课程/讲义:2026.mp4",stored.path("relativePath").asText());
        JsonNode frozen=quark.expand(Set.of("colon-file"),null,null).get(0);assertEquals("Quark/12345678/课程/讲义:2026.mp4",frozen.path("plannedRelativePath").asText());
        String local=quark.localPath(frozen);assertEquals("Quark/12345678/课程/讲义_2026.mp4",local);assertFalse(local.contains(":"));assertEquals(local,quark.localPath(frozen));
    }
}
