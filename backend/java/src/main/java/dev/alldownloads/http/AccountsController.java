package dev.alldownloads.http;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.business.*;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import static dev.alldownloads.business.Json.*;

@RestController
@RequestMapping("/api/v1")
public final class AccountsController {
    private final BusinessApplication app;
    public AccountsController(BusinessApplication app) {this.app=app;}
    private ResponseEntity<JsonNode> response(int status,ObjectNode data) {
        var response=ResponseEntity.status(status);if(data.path("revision").isTextual())response.eTag(text(data,"revision"));
        return response.body(object("data",data,"requestId",id()));
    }
    private ResponseEntity<JsonNode> write(String scope,String key,JsonNode body,BusinessStore.Work<ObjectNode> work) {
        return response(202,app.store.tx(()->app.store.once(scope,key,body,work)));
    }
    @GetMapping("/accounts") public ResponseEntity<JsonNode> accounts() {return response(200,app.store.tx(app.auth::accounts));}
    @PostMapping("/accounts/{id}/check") public ResponseEntity<JsonNode> check(@PathVariable String id,@RequestHeader("Idempotency-Key") String key,@RequestBody(required=false) JsonNode body) {
        if(body!=null&&(!body.isObject()||body.size()!=0))throw Failure.bad("INVALID_INPUT","Account check accepts no credentials or options");
        return write("account-check:"+id,key,object(),()->app.auth.check(id));
    }
    @PostMapping("/auth-sessions") public ResponseEntity<JsonNode> begin(@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) {return write("auth-begin",key,body,()->app.auth.begin(body));}
    @GetMapping("/auth-sessions/{id}") public ResponseEntity<JsonNode> session(@PathVariable String id) {return response(200,app.store.tx(()->app.auth.session(id)));}
    @PostMapping("/auth-sessions/{id}/steps") public ResponseEntity<JsonNode> poll(@PathVariable String id,@RequestHeader("Idempotency-Key") String key,@RequestBody JsonNode body) {return write("auth-poll:"+id,key,body,()->app.auth.poll(id,body));}
    @DeleteMapping("/auth-sessions/{id}") public ResponseEntity<JsonNode> cancel(@PathVariable String id,@RequestHeader("If-Match") String revision) {
        if(revision.length()<3||!revision.startsWith("\"")||!revision.endsWith("\""))throw Failure.bad("INVALID_INPUT","If-Match must use the quoted revision");
        return response(202,app.store.tx(()->app.auth.cancel(id,revision.substring(1,revision.length()-1))));
    }
}
