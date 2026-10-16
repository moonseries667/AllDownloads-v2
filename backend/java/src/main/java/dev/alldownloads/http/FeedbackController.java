package dev.alldownloads.http;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.business.*;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import java.util.*;
import static dev.alldownloads.business.Json.*;

@RestController
@RequestMapping("/api/v1")
public final class FeedbackController {
    private final BusinessApplication app;
    public FeedbackController(BusinessApplication app){this.app=app;}
    private ResponseEntity<JsonNode> read(BusinessStore.Work<? extends JsonNode> work){return ResponseEntity.ok(object("data",app.store.tx(work),"requestId",id()));}
    private ResponseEntity<JsonNode> write(String scope,String key,JsonNode body,BusinessStore.Work<ObjectNode> work){return ResponseEntity.status(202).body(object("data",app.store.tx(()->app.store.once(scope,key,body,work)),"requestId",id()));}
    @PostMapping("/telegram/saved-sync") public ResponseEntity<JsonNode> saved(@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body){return write("saved-sync",key,body,()->app.feedback.savedSync(body));}
    @GetMapping("/telegram/messages") public ResponseEntity<JsonNode> messages(@RequestParam Map<String,String> query){return read(()->app.feedback.messages(query));}
    @GetMapping("/telegram/messages/{id}") public ResponseEntity<JsonNode> message(@PathVariable String id){return read(()->app.feedback.telegram.message(id));}
    @GetMapping("/telegram/watcher") public ResponseEntity<JsonNode> watcher(@RequestParam String accountId){return watcherResponse(app.store.tx(()->app.feedback.telegram.watcher(accountId)));}
    @PatchMapping("/telegram/watcher") public ResponseEntity<JsonNode> watcher(@RequestHeader("If-Match") String revision,@RequestBody JsonNode body){if(!body.path("enabled").isBoolean())throw Failure.bad("INVALID_INPUT","enabled is required");return watcherResponse(app.store.tx(()->app.feedback.telegram.setWatcher(required(body,"accountId"),body.path("enabled").asBoolean(),etag(revision))));}
    private ResponseEntity<JsonNode> watcherResponse(ObjectNode data){return ResponseEntity.ok().eTag(text(data,"revision")).body(object("data",data,"requestId",id()));}
    @PostMapping("/telegram/retry-incomplete") public ResponseEntity<JsonNode> retry(@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body){return write("tel-retry",key,body,()->app.feedback.retryIncomplete(body));}
    @PostMapping("/telegram/link-downloads") public ResponseEntity<JsonNode> removed(){throw new Failure(410,"FEATURE_REMOVED","Telegram 仅提供收藏夹增量下载。");}
    @GetMapping("/issues") public ResponseEntity<JsonNode> issues(@RequestParam Map<String,String> query){return read(()->{var values=list(app.feedback.issues.list(Boolean.parseBoolean(query.getOrDefault("includeResolved","false"))));return CatalogService.page(values,query,PathPlanner.digest(canonical(array(values))));});}
    @PostMapping("/issues/{id}/acknowledge") public ResponseEntity<JsonNode> acknowledge(@PathVariable String id){return read(()->app.feedback.issues.acknowledge(id));}
    @GetMapping("/quark/folders") public ResponseEntity<JsonNode> folders(@RequestParam Map<String,String> query){return read(()->app.feedback.folders(query));}
    @PostMapping("/quark/shares") public ResponseEntity<JsonNode> shares(@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body){return write("quark-shares",key,body,()->app.feedback.addShares(body));}
    @GetMapping("/quark/shares") public ResponseEntity<JsonNode> shares(@RequestParam Map<String,String> query){return read(()->CatalogService.page(list(app.feedback.quark.shares()),query,"quark-shares"));}
    @GetMapping("/quark/shares/{id}") public ResponseEntity<JsonNode> share(@PathVariable String id){return read(()->app.feedback.quark.share(id));}
    @PatchMapping("/quark/shares/{id}") public ResponseEntity<JsonNode> watch(@PathVariable String id,@RequestHeader("If-Match") String revision,@RequestBody JsonNode body){if(!body.path("watchEnabled").isBoolean())throw Failure.bad("INVALID_INPUT","watchEnabled required");ObjectNode value=app.store.tx(()->app.feedback.quark.setWatcher(id,body.path("watchEnabled").asBoolean(),etag(revision)));return ResponseEntity.ok().eTag(text(value,"revision")).body(object("data",value,"requestId",Json.id()));}
    @PostMapping("/quark/shares/{id}/refresh") public ResponseEntity<JsonNode> checkShare(@PathVariable String id,@RequestHeader("Idempotency-Key") String key){return write("quark-refresh:"+id,key,object(),()->app.feedback.refreshShare(id));}
    @PostMapping("/quark/shares/{id}/refresh-tree") public ResponseEntity<JsonNode> refresh(@PathVariable String id,@RequestHeader("Idempotency-Key") String key){return write("quark-tree:"+id,key,object(),()->app.feedback.refreshTree(id));}
    @GetMapping("/quark/nodes") public ResponseEntity<JsonNode> nodes(@RequestParam Map<String,String> query){return read(()->{String share=query.get("shareId");if(share==null)throw Failure.bad("INVALID_INPUT","shareId is required");List<JsonNode> values=list(app.feedback.quark.nodes(share));if(query.containsKey("parentId"))values.removeIf(n->!text(n,"parentId").equals(query.get("parentId")));String q=query.getOrDefault("q","").toLowerCase(Locale.ROOT);if(!q.isEmpty())values.removeIf(n->!text(n,"name").toLowerCase(Locale.ROOT).contains(q));return CatalogService.page(values,query,PathPlanner.digest(canonical(array(values))));});}
    private static String etag(String value){if(value.length()>1&&value.startsWith("\"")&&value.endsWith("\""))return value.substring(1,value.length()-1);throw Failure.bad("INVALID_INPUT","If-Match must use the quoted revision");}
}
