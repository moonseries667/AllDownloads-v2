package dev.alldownloads.http;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.business.*;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import java.net.InetAddress;
import java.util.Map;
import static dev.alldownloads.business.Json.*;

@RestController
@RequestMapping("/api/v1")
public final class ManagementController {
    private final BusinessApplication app;
    public ManagementController(BusinessApplication app) {this.app=app;}
    private ResponseEntity<JsonNode> read(BusinessStore.Work<ObjectNode> work) {
        ObjectNode value=app.store.tx(work);var response=ResponseEntity.ok();if(value.path("revision").isTextual())response.eTag(text(value,"revision"));
        return response.body(object("data",value,"requestId",id()));
    }
    private ResponseEntity<JsonNode> write(String scope,String key,JsonNode request,BusinessStore.Work<ObjectNode> work) {
        return ResponseEntity.status(202).body(object("data",app.store.tx(()->app.store.once(scope,key,request,work)),"requestId",id()));
    }
    @GetMapping("/download-projects") public ResponseEntity<JsonNode> projects(@RequestParam Map<String,String> query) {return read(()->{app.queue.expire();return app.management.projects(query);});}
    @GetMapping("/download-projects/{id}") public ResponseEntity<JsonNode> project(@PathVariable String id) {return read(()->{app.queue.expire();return app.management.project(id);});}
    @PostMapping("/download-projects/{id}/actions") public ResponseEntity<JsonNode> cancelProject(@PathVariable String id,@RequestHeader("If-Match") String revision,@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) {
        cancelOnly(body);return write("cancel-project:"+id,key,object("body",body,"revision",revision),()->app.management.cancel(id,null,etag(revision)));
    }
    @PostMapping("/download-projects/{id}/items/{itemId}/actions") public ResponseEntity<JsonNode> cancelChild(@PathVariable String id,@PathVariable String itemId,@RequestHeader("If-Match") String revision,@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) {
        cancelOnly(body);return write("cancel-child:"+id+":"+itemId,key,object("body",body,"revision",revision),()->app.management.cancel(id,itemId,etag(revision)));
    }
    @GetMapping("/archives") public ResponseEntity<JsonNode> archives(@RequestParam Map<String,String> query) {return read(()->app.management.archives(query));}
    @PostMapping("/directory-selections") public ResponseEntity<JsonNode> selectDirectory(HttpServletRequest request,@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) throws Exception {
        if(!InetAddress.getByName(request.getRemoteAddr()).isLoopbackAddress())throw new Failure(409,"NATIVE_PICKER_UNAVAILABLE","目录选择必须在运行主机的浏览器操作");
        if(!body.isObject()||body.size()!=0)throw Failure.bad("INVALID_INPUT","Directory selection accepts no client path");
        return write("directory-selection",key,body,app.storageSettings::selectDirectory);
    }
    @GetMapping("/directory-selections/{id}") public ResponseEntity<JsonNode> selection(@PathVariable String id) {return read(()->app.store.get("directorySelection",id));}
    @PostMapping("/directory-selections/{id}/cancel") public ResponseEntity<JsonNode> cancelSelection(@PathVariable String id,@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) {return write("cancel-selection:"+id,key,body,()->app.storageSettings.cancelSelection(id));}
    @PostMapping("/settings/validate") public ResponseEntity<JsonNode> validate(@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) {return write("settings-validation",key,body,()->app.storageSettings.validate(body));}
    @GetMapping("/settings/validations/{id}") public ResponseEntity<JsonNode> validation(@PathVariable String id) {return read(()->app.store.get("settingsValidation",id));}
    @PatchMapping("/settings") public ResponseEntity<JsonNode> save(@RequestHeader("If-Match") String revision,@RequestBody JsonNode body) {return read(()->app.storageSettings.save(etag(revision),body));}
    @GetMapping("/drive/folders") public ResponseEntity<JsonNode> folders(@RequestParam Map<String,String> query) {return read(()->app.storageSettings.folders(query));}
    private static void cancelOnly(JsonNode body) {if(body.size()!=1||!required(body,"action").equals("CANCEL"))throw Failure.bad("INVALID_INPUT","Only CANCEL is supported for download projects");}
    private static String etag(String value) {if(value.startsWith("\"")&&value.endsWith("\""))return value.substring(1,value.length()-1);throw Failure.bad("INVALID_INPUT","If-Match must use the quoted revision");}
}
