package dev.alldownloads.http;

import com.fasterxml.jackson.databind.JsonNode;
import dev.alldownloads.business.*;
import org.springframework.web.bind.annotation.*;
import static dev.alldownloads.business.Json.*;

@RestController
@RequestMapping("/internal/v1")
public class RunnerController {
    private final BusinessApplication app;
    public RunnerController(BusinessApplication app) {this.app=app;}
    private JsonNode run(BusinessStore.Work<? extends JsonNode> work) {return object("data",app.store.tx(work),"requestId",id());}
    @PostMapping("/runners/register") public JsonNode register(@RequestBody JsonNode body) {return run(()->app.queue.register(body));}
    @PostMapping("/leases/acquire") public JsonNode acquire(@RequestBody JsonNode body) {return run(()->app.queue.acquire(body));}
    @PostMapping("/leases/{id}/heartbeat") public JsonNode heartbeat(@PathVariable String id,@RequestBody JsonNode body) {return run(()->app.queue.heartbeat(id,body));}
    @PostMapping("/operations/{id}/facts") public JsonNode facts(@PathVariable String id,@RequestBody JsonNode body) {return run(()->app.queue.facts(id,body));}
    @PostMapping("/operations/{id}/result") public JsonNode result(@PathVariable String id,@RequestBody JsonNode body) {return run(()->app.queue.result(id,body,false));}
    @PostMapping("/operations/{id}/reconcile") public JsonNode reconcile(@PathVariable String id,@RequestBody JsonNode body) {return run(()->app.queue.result(id,body,true));}
}
