package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import java.nio.file.Path;
import java.util.*;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class StorageSettingsTest {
    @TempDir Path temp;
    BusinessFlowTest f;
    @BeforeEach void start() throws Exception {f=new BusinessFlowTest();f.temp=temp;f.open();register(true);}
    @AfterEach void close() throws Exception {f.close();}
    void register(boolean picker) {f.store.tx(()->f.app.queue.register(object("runnerId","runner","protocolVersion",1,"capabilities",List.of("VALIDATE_TARGET","SELECT_LOCAL_DIRECTORY","LIST_DRIVE_FOLDERS","VALIDATE_DRIVE_TARGET","DOWNLOAD_MEDIA"),"tools",object(),"facts",object("directoryPicker",object("available",picker,"reasonCode",picker?null:"NON_INTERACTIVE_SESSION","hostId","test-host")))));}
    ObjectNode settings() {return f.store.tx(f.app.storageSettings::settings);}
    ObjectNode folder(String id,String parent,String name) {return object("id",id,"accountId","account","parentId",parent,"name",name,"kind","FOLDER","revision","r1","selectable",true,"availability","AVAILABLE","availabilityReason",null,"path",parent==null?"":name);}
    ObjectNode browse() {return f.store.tx(()->f.app.storageSettings.folders(Map.of("accountId","account")));}
    void directoryPage(JsonNode op,List<JsonNode> children,String next) {ObjectNode root=folder("root",null,"我的云端硬盘");f.complete(op,object("accountId","account","parentId",null,"items",children,"parent",root,"breadcrumbs",List.of(root),"nextCursor",next));}
    @Test void nativeCancelAndUnavailableLeaveGlobalSettingsUntouched() {
        ObjectNode before=settings(),selection=f.store.tx(f.app.storageSettings::selectDirectory);JsonNode op=f.acquire("SELECT_LOCAL_DIRECTORY");
        f.complete(op,object("selectionId",selection.path("selectionId"),"cancelled",true,"target",null));assertEquals("CANCELLED",text(f.store.tx(()->f.store.get("directorySelection",text(selection,"selectionId"))),"status"));assertEquals(before,settings());
        register(false);ObjectNode unavailable=f.store.tx(f.app.storageSettings::selectDirectory);assertTrue(unavailable.path("taskId").isNull());assertEquals("UNAVAILABLE",text(f.store.tx(()->f.store.get("directorySelection",text(unavailable,"selectionId"))),"status"));assertEquals(before.path("defaultTarget"),settings().path("defaultTarget"));
        f.store.tx(()->f.app.queue.register(object("runnerId","runner","protocolVersion",1,"capabilities",List.of("VALIDATE_TARGET"),"tools",object())));
        ObjectNode noDrive=browse();assertEquals("NEEDS_ACTION",text(noDrive,"status"));assertEquals("AUTH_REQUIRED",text(noDrive.path("error"),"code"));assertTrue(noDrive.path("taskId").isNull());
    }
    @Test void lateNativeSelectionAfterUserCancelCannotChangeOrCreateTarget() {
        ObjectNode request=f.store.tx(f.app.storageSettings::selectDirectory);JsonNode op=f.acquire("SELECT_LOCAL_DIRECTORY");f.store.tx(()->f.app.storageSettings.cancelSelection(text(request,"selectionId")));
        f.complete(op,object("selectionId",request.path("selectionId"),"cancelled",false,"target",object("canonicalPath",temp.resolve("new").toString(),"rootId","new-root","writable",true)));
        assertEquals(1,f.store.tx(()->f.store.all("target").size()));assertEquals("CANCELLED",text(f.store.tx(()->f.store.get("directorySelection",text(request,"selectionId"))),"status"));
    }
    @Test void selectedNativeDirectoryMustBeValidatedSavedAndRemainFrozenAfterRestart() throws Exception {
        ObjectNode order=f.submit(f.preview(f.request("audio-fixture"))),before=settings();ObjectNode selection=f.store.tx(f.app.storageSettings::selectDirectory);JsonNode op=f.acquire("SELECT_LOCAL_DIRECTORY");
        f.complete(op,object("selectionId",selection.path("selectionId"),"cancelled",false,"target",object("canonicalPath",temp.resolve("selected").toString(),"rootId","selected-root","writable",true)));
        String target=f.store.tx(()->f.store.get("directorySelection",text(selection,"selectionId"))).path("targetId").asText();assertEquals(before.path("downloadRoot"),settings().path("downloadRoot"));
        ObjectNode validated=f.store.tx(()->f.app.storageSettings.validate(object("downloadTarget",object("id",target,"revision","1"),"archiveDestination",null)));
        ObjectNode saved=f.store.tx(()->f.app.storageSettings.save("1",object("validationId",validated.path("validationId"))));assertEquals("2",text(saved,"revision"));assertEquals(temp.resolve("selected").toString(),text(saved,"downloadRoot"));
        BusinessFlowTest.assertCode("VERSION_CHANGED",()->f.store.tx(()->f.app.storageSettings.save("1",object("validationId",validated.path("validationId")))));
        JsonNode download=f.acquire("DOWNLOAD_MEDIA");assertEquals(before.path("downloadRoot"),download.path("destination").path("rootPath"));assertEquals(before.path("defaultTarget"),f.store.tx(()->f.app.downloads.order(text(order,"orderId"))).path("target"));
        f.store.close();f.start();assertEquals(saved.path("downloadRoot"),settings().path("downloadRoot"));assertEquals("2",text(settings(),"revision"));
    }
    @Test void sameNameDriveFoldersHaveIndependentIdsPaginationAndValidatedTarget() {
        ObjectNode loading=browse();assertEquals("LOADING",text(loading,"status"));assertEquals(loading,browse());JsonNode op=f.acquire("LIST_DRIVE_FOLDERS");
        directoryPage(op,List.of(folder("one","root","相同名称"),folder("two","root","相同名称")),"provider-page-2");ObjectNode page=browse();assertEquals(2,page.path("items").size());assertNotEquals(page.path("items").get(0).path("id"),page.path("items").get(1).path("id"));
        String cursor=text(page,"nextCursor");BusinessFlowTest.assertCode("INVALID_CURSOR",()->f.store.tx(()->f.app.storageSettings.folders(Map.of("accountId","another","cursor",cursor))));
        ObjectNode next=f.store.tx(()->f.app.storageSettings.folders(Map.of("accountId","account","cursor",cursor)));assertEquals("LOADING",text(next,"status"));JsonNode op2=f.acquire("LIST_DRIVE_FOLDERS");assertEquals("provider-page-2",text(op2.path("payload"),"cursor"));directoryPage(op2,List.of(),null);
        ObjectNode request=object("downloadTarget",object("id","default","revision","1"),"archiveDestination",object("accountId","account","folderId","two","revision","r1"));ObjectNode validation=f.store.tx(()->f.app.storageSettings.validate(request));
        BusinessFlowTest.assertCode("SETTINGS_NOT_VALIDATED",()->f.store.tx(()->f.app.storageSettings.save("1",object("validationId",validation.path("validationId")))));
        JsonNode check=f.acquire("VALIDATE_DRIVE_TARGET");f.complete(check,object("validationId",validation.path("validationId"),"target",object("accountId","account","folderId","two","revision","r1","name","相同名称","path","相同名称"),"writable",true));
        ObjectNode saved=f.store.tx(()->f.app.storageSettings.save("1",object("validationId",validation.path("validationId"))));assertEquals("two",text(saved.path("archiveDestination"),"folderId"));
    }
    @Test void permissionChangeFailsValidationWithoutSavingAndBrokenParentChainIsRejected() {
        browse();JsonNode list=f.acquire("LIST_DRIVE_FOLDERS");ObjectNode invalid=folder("one","wrong","叶目录");
        BusinessFlowTest.assertCode("PROVIDER_CHANGED",()->directoryPage(list,List.of(invalid),null));directoryPage(list,List.of(folder("one","root","叶目录")),null);
        ObjectNode validation=f.store.tx(()->f.app.storageSettings.validate(object("downloadTarget",object("id","default","revision","1"),"archiveDestination",object("accountId","account","folderId","one","revision","r1"))));
        JsonNode op=f.acquire("VALIDATE_DRIVE_TARGET");ObjectNode failure=f.result(op,null);failure.put("status","NEEDS_ACTION");failure.set("error",object("code","TARGET_PERMISSION_DENIED","completedEffects",List.of(),"pendingEffects",List.of()));f.store.tx(()->f.app.queue.result(text(op,"operationId"),failure,false));
        assertEquals("INVALID",text(f.store.tx(()->f.store.get("settingsValidation",text(validation,"validationId"))),"status"));assertTrue(settings().path("archiveDestination").isNull());
    }
}
