package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.sql.SQLException;
import java.time.Instant;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** Validated settings snapshots. Selecting or validating a directory never saves settings. */
public final class StorageSettings {
    private final BusinessStore store;
    private final OperationQueue queue;
    public StorageSettings(BusinessStore store, OperationQueue queue) { this.store=store; this.queue=queue; }

    public ObjectNode settings() throws SQLException {
        ObjectNode saved=store.optional("settings","global"), target=store.get("target","default");
        return object("revision",saved==null?"1":text(saved,"revision"),"downloadRoot",target.path("path"),
            "storageRevision",target.path("revision"),"timezone","Asia/Shanghai","defaultTarget",target,
            "archiveDestination",saved==null?null:saved.path("archiveDestination"),
            "archiveDestinationLabel",saved==null||!saved.path("archiveDestination").isObject()?null:saved.path("archiveDestination").path("path"),
            "directoryPicker",picker());
    }
    public JsonNode frozenArchiveTarget() throws SQLException { return settings().path("archiveDestination").deepCopy(); }
    public ObjectNode picker() throws SQLException {
        JsonNode newest=store.all("runner").stream().max(Comparator.comparing(r->text(r,"lastSeenAt"))).orElse(null);
        if(newest==null||!Instant.parse(text(newest,"lastSeenAt")).isAfter(store.clock.instant().minusSeconds(90)))
            return object("available",false,"reasonCode","RUNNER_UNAVAILABLE","hostId",null);
        JsonNode fact=newest.path("facts").path("directoryPicker");
        boolean available=fact.path("available").asBoolean(false)&&strings(newest.path("capabilities")).contains("SELECT_LOCAL_DIRECTORY")&&!text(fact,"hostId").isBlank();
        return object("available",available,"reasonCode",available?null:text(fact,"reasonCode").isBlank()?"NATIVE_PICKER_UNAVAILABLE":text(fact,"reasonCode"),"hostId",fact.path("hostId").isTextual()?fact.path("hostId"):null);
    }
    public ObjectNode selectDirectory() throws SQLException {
        String selection=id();ObjectNode capability=picker();String task=null;
        if(capability.path("available").asBoolean()) task=queue.enqueue("SELECT_LOCAL_DIRECTORY","BILIBILI","picker:"+selection,null,text(settings(),"storageRevision"),
            object("schemaVersion",1,"selectionId",selection,"title","选择本机下载目录","timeoutSeconds",120));
        store.put("directorySelection",selection,object("id",selection,"status",task==null?"UNAVAILABLE":"PENDING","targetId",null,
            "error",task==null?error(text(capability,"reasonCode")):null));
        if(task!=null)store.put("directorySelectionTask",selection,object("taskId",task));
        return object("selectionId",selection,"taskId",task);
    }
    public ObjectNode cancelSelection(String id) throws SQLException {
        ObjectNode selection=store.get("directorySelection",id);
        if(!text(selection,"status").equals("PENDING"))return selection;
        JsonNode mapping=store.optional("directorySelectionTask",id);
        if(mapping!=null) {ObjectNode task=store.get("task",text(mapping,"taskId"));if(strings(task.path("allowedActions")).contains("CANCEL"))queue.control(text(task,"id"),text(task,"revision"),"CANCEL");}
        selection.put("status","CANCELLED");store.put("directorySelection",id,selection);return selection;
    }
    public void selectionResult(JsonNode op,JsonNode result) throws SQLException {
        String id=required(op.path("payload"),"selectionId");if(!id.equals(required(result,"selectionId")))throw Failure.conflict("VERIFICATION_FAILED");
        ObjectNode selection=store.get("directorySelection",id);
        if(text(selection,"status").equals("CANCELLED"))return;
        if(!result.path("cancelled").isBoolean())throw Failure.conflict("VERIFICATION_FAILED");
        if(result.path("cancelled").asBoolean()) {if(!result.path("target").isNull())throw Failure.conflict("VERIFICATION_FAILED");selection.put("status","CANCELLED");}
        else {
            JsonNode selected=result.path("target");String path=required(selected,"canonicalPath");DownloadService.validatePath(path);
            if(!selected.path("writable").asBoolean(false))throw Failure.conflict("VERIFICATION_FAILED");
            String targetId=id();store.put("target",targetId,object("id",targetId,"revision","1","status","VALID","path",path,"label",path,
                "rootId",required(selected,"rootId"),"archivePolicy","NONE","error",null));
            selection.put("status","SELECTED");selection.put("targetId",targetId);
        }
        store.put("directorySelection",id,selection);
    }
    public ObjectNode validate(JsonNode request) throws SQLException {
        JsonNode ref=request.path("downloadTarget");ObjectNode target=store.get("target",required(ref,"id"));
        if(!text(target,"revision").equals(required(ref,"revision")))throw Failure.conflict("STORAGE_CHANGED");
        if(!text(target,"status").equals("VALID"))throw new Failure(422,"INVALID_STORAGE_ROOT","目录尚未通过执行程序验证");
        if(!request.has("archiveDestination"))throw Failure.bad("INVALID_INPUT","archiveDestination is required");
        JsonNode drive=request.path("archiveDestination");String validation=id(),task=null;
        JsonNode snapshot=drive;
        if(!drive.isNull()) {
            String account=required(drive,"accountId"),folder=required(drive,"folderId"),revision=required(drive,"revision");
            ObjectNode node=store.get("driveFolder",account+":"+folder);
            if(!revision.equals(text(node,"revision")))throw Failure.conflict("TARGET_CHANGED");
            if(!node.path("selectable").asBoolean(false)||!text(node,"availability").equals("AVAILABLE"))throw new Failure(422,"TARGET_PERMISSION_DENIED","上传目录不可写");
            snapshot=object("accountId",account,"folderId",folder,"revision",revision,"name",node.path("name"),"path",node.path("path"));
            task=queue.enqueue("VALIDATE_DRIVE_TARGET","BILIBILI","settings-validation:"+validation,null,text(settings(),"storageRevision"),
                object("schemaVersion",1,"validationId",validation,"accountId",account,"folderId",folder,"revision",revision));
        }
        store.put("settingsValidation",validation,object("id",validation,"revision",settings().path("revision"),"status",task==null?"VALID":"VALIDATING",
            "downloadTarget",target,"archiveDestination",snapshot,"error",null));
        return object("validationId",validation,"taskId",task);
    }
    public void driveValidationResult(JsonNode op,JsonNode result) throws SQLException {
        JsonNode payload=op.path("payload"),target=result.path("target");String id=required(payload,"validationId");
        if(!id.equals(required(result,"validationId"))||!result.path("writable").asBoolean(false))throw Failure.conflict("VERIFICATION_FAILED");
        for(String field:List.of("accountId","folderId","revision"))if(!required(payload,field).equals(required(target,field)))throw Failure.conflict("TARGET_CHANGED");
        required(target,"name");if(!target.path("path").isTextual())throw Failure.conflict("VERIFICATION_FAILED");
        ObjectNode validation=store.get("settingsValidation",id);validation.set("archiveDestination",target);validation.put("status","VALID");store.put("settingsValidation",id,validation);
    }
    public ObjectNode save(String revision,JsonNode request) throws SQLException {
        ObjectNode current=settings();if(!text(current,"revision").equals(revision))throw Failure.conflict("VERSION_CHANGED");
        ObjectNode validated=store.get("settingsValidation",required(request,"validationId"));
        if(!text(validated,"revision").equals(revision))throw Failure.conflict("VERSION_CHANGED");
        if(!text(validated,"status").equals("VALID"))throw new Failure(422,"SETTINGS_NOT_VALIDATED","设置尚未通过验证");
        JsonNode source=validated.path("downloadTarget");
        if(!source.equals(store.get("target",text(source,"id"))))throw Failure.conflict("STORAGE_CHANGED");
        JsonNode drive=validated.path("archiveDestination");
        if(drive.isObject()) {ObjectNode latest=store.get("driveFolder",text(drive,"accountId")+":"+text(drive,"folderId"));if(!text(latest,"revision").equals(text(drive,"revision"))||!latest.path("selectable").asBoolean())throw Failure.conflict("TARGET_CHANGED");}
        ObjectNode target=(ObjectNode)source.deepCopy();target.put("id","default");target.put("revision",Long.toString(Long.parseLong(text(current,"storageRevision"))+1));target.put("archivePolicy","AUTO");
        store.put("target","default",target);store.put("settings","global",object("revision",Long.toString(Long.parseLong(revision)+1),"archiveDestination",drive));
        return settings();
    }
    public ObjectNode folders(Map<String,String> query) throws SQLException {
        String account=query.get("accountId");if(account==null||account.isBlank())throw Failure.bad("INVALID_INPUT","accountId is required");
        String parent=query.get("parentId"),cursor=null;int size;
        try {size=Integer.parseInt(query.getOrDefault("pageSize","50"));}catch(Exception e){throw Failure.bad("INVALID_INPUT","Invalid pageSize");}
        if(size<1||size>200)throw Failure.bad("INVALID_INPUT","Invalid pageSize");
        if(query.containsKey("cursor")) {
            ObjectNode c=store.optional("driveCursor",query.get("cursor"));
            if(c==null||!text(c,"accountId").equals(account)||!Objects.equals(c.path("parentId").isNull()?null:text(c,"parentId"),parent)||c.path("pageSize").asInt()!=size)throw Failure.bad("INVALID_CURSOR","目录分页范围已改变");
            cursor=required(c,"providerCursor");
        }
        String key=PathPlanner.digest(canonical(object("account",account,"parent",parent,"cursor",cursor,"size",size)));
        ObjectNode page=store.optional("drivePage",key),observed=store.optional("drivePageObserved",key);
        if(page!=null&&(text(page,"status").equals("LOADING")||observed!=null&&observed.path("expires").asLong()>store.millis()))return page;
        String task=queue.enqueue("LIST_DRIVE_FOLDERS","BILIBILI","drive-page:"+key,null,text(settings(),"storageRevision"),
            object("schemaVersion",1,"accountId",account,"parentId",parent,"cursor",cursor,"pageSize",size));
        store.put("drivePageKey",task,object("key",key));
        page=object("items",List.of(),"nextCursor",null,"total",null,"parent",null,"breadcrumbs",List.of(),"status","LOADING","taskId",task,"error",null);
        store.put("drivePage",key,page);return page;
    }
    public void foldersResult(JsonNode op,JsonNode result) throws SQLException {
        JsonNode payload=op.path("payload");String account=required(payload,"accountId");
        if(!account.equals(required(result,"accountId"))||!payload.path("parentId").equals(result.path("parentId"))||!result.path("items").isArray()||!result.path("breadcrumbs").isArray())throw Failure.conflict("PROVIDER_CHANGED");
        JsonNode parent=result.path("parent");validateFolder(parent,account);
        if(payload.path("parentId").isTextual()&&!text(parent,"id").equals(text(payload,"parentId")))throw Failure.conflict("PROVIDER_CHANGED");
        if(payload.path("parentId").isNull()&&!parent.path("parentId").isNull())throw Failure.conflict("PROVIDER_CHANGED");
        Set<String> chain=new HashSet<>();String previous=null;
        for(JsonNode ancestor:result.path("breadcrumbs")) {validateFolder(ancestor,account);if(!chain.add(text(ancestor,"id"))||!Objects.equals(ancestor.path("parentId").isNull()?null:text(ancestor,"parentId"),previous))throw Failure.conflict("PROVIDER_CHANGED");previous=text(ancestor,"id");store.put("driveFolder",account+":"+previous,ancestor);}
        if(!text(parent,"id").equals(previous)||!parent.equals(result.path("breadcrumbs").get(result.path("breadcrumbs").size()-1)))throw Failure.conflict("PROVIDER_CHANGED");
        Set<String> ids=new HashSet<>();for(JsonNode item:result.path("items")) {validateFolder(item,account);if(!text(item,"parentId").equals(text(parent,"id"))||!ids.add(text(item,"id")))throw Failure.conflict("PROVIDER_CHANGED");store.put("driveFolder",account+":"+text(item,"id"),item);}
        String next=null;if(result.path("nextCursor").isTextual()) {next=id();store.put("driveCursor",next,object("accountId",account,"parentId",payload.path("parentId"),"pageSize",payload.path("pageSize"),"providerCursor",required(result,"nextCursor")));}
        else if(!result.path("nextCursor").isNull())throw Failure.conflict("PROVIDER_CHANGED");
        String key=required(store.get("drivePageKey",text(op,"taskId")),"key");
        store.put("drivePage",key,object("items",result.path("items"),"nextCursor",next,"total",null,"parent",parent,"breadcrumbs",result.path("breadcrumbs"),"status","READY","taskId",null,"error",null));
        store.put("drivePageObserved",key,object("expires",store.millis()+60_000));
    }
    private static void validateFolder(JsonNode node,String account) {
        if(!required(node,"accountId").equals(account)||!required(node,"kind").equals("FOLDER"))throw Failure.conflict("PROVIDER_CHANGED");
        required(node,"id");required(node,"revision");String name=required(node,"name");
        if(name.contains("/")||name.contains("\\")||!node.path("path").isTextual()||!node.path("selectable").isBoolean())throw Failure.conflict("PROVIDER_CHANGED");
        required(node,"availability");
    }
    public void outcome(JsonNode op,JsonNode request) throws SQLException {
        if(text(request,"status").equals("SUCCEEDED"))return;
        String code=text(request.path("error"),"code");if(code.isBlank())code="NATIVE_PICKER_UNAVAILABLE";
        switch(text(op,"operationType")) {
            case "SELECT_LOCAL_DIRECTORY" -> {String id=text(op.path("payload"),"selectionId");ObjectNode selection=store.get("directorySelection",id);if(text(selection,"status").equals("PENDING")){selection.put("status",text(request,"status").equals("CANCELLED")?"CANCELLED":"UNAVAILABLE");selection.set("error",error(code));store.put("directorySelection",id,selection);}}
            case "VALIDATE_DRIVE_TARGET" -> {String id=text(op.path("payload"),"validationId");ObjectNode validation=store.get("settingsValidation",id);validation.put("status","INVALID");validation.set("error",error(code));store.put("settingsValidation",id,validation);}
            case "LIST_DRIVE_FOLDERS" -> {String key=text(store.get("drivePageKey",text(op,"taskId")),"key");ObjectNode page=store.get("drivePage",key);page.put("status","NEEDS_ACTION");page.set("error",error(code));store.put("drivePage",key,page);store.put("drivePageObserved",key,object("expires",store.millis()+60_000));}
            default -> { }
        }
    }
    private static ObjectNode error(String code) {return object("code",code,"message",OperationQueue.message(code));}
}
