package dev.alldownloads.http;

import static dev.alldownloads.business.Json.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.business.*;
import java.util.*;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/v1")
public class ApiController {
  private final BusinessApplication app;

  public ApiController(BusinessApplication app) {
    this.app = app;
  }

  private ResponseEntity<JsonNode> read(BusinessStore.Work<? extends JsonNode> work) {
    return ResponseEntity.ok(object("data", app.store.tx(work), "requestId", id()));
  }

  private ResponseEntity<JsonNode> write(
      String scope, String key, JsonNode body, BusinessStore.Work<ObjectNode> work) {
    return ResponseEntity.status(202)
        .body(
            object(
                "data",
                app.store.tx(() -> app.store.once(scope, key, body, work)),
                "requestId",
                id()));
  }

  @GetMapping({"/health", "/overview"})
  public ResponseEntity<JsonNode> overview() {
    return read(app::overview);
  }

  @GetMapping("/settings")
  public ResponseEntity<JsonNode> settings() {
    return read(app.downloads::settings);
  }

  @GetMapping("/bilibili/sources")
  public ResponseEntity<JsonNode> sources(@RequestParam Map<String, String> query) {
    return read(
        () ->
            CatalogService.page(
                app.catalog.sources().stream().map(app.catalog::publicSource).toList(),
                query,
                "sources"));
  }

  @PostMapping("/bilibili/sources")
  public ResponseEntity<JsonNode> addSources(
      @RequestHeader("Idempotency-Key") String key, @RequestBody JsonNode body) {
    return write("sources", key, body, () -> app.catalog.addSources(body));
  }

  @PatchMapping("/bilibili/sources/{id}")
  public ResponseEntity<JsonNode> patchSource(
      @PathVariable String id,
      @RequestHeader("If-Match") String revision,
      @RequestBody JsonNode body) {
    ObjectNode source =
        app.store.tx(
            () -> app.catalog.publicSource(app.catalog.sourcePatch(id, etag(revision), body)));
    return ResponseEntity.ok()
        .eTag(text(source, "revision"))
        .body(object("data", source, "requestId", Json.id()));
  }

  @PostMapping("/bilibili/sources/{id}/refresh")
  public ResponseEntity<JsonNode> refresh(
      @PathVariable String id,
      @RequestHeader("Idempotency-Key") String key,
      @RequestBody JsonNode body) {
    if (!body.path("force").isBoolean())
      throw Failure.bad("INVALID_INPUT", "force must be boolean");
    return write(
        "refresh:" + id, key, body, () -> app.catalog.refresh(id, body.path("force").asBoolean()));
  }

  @GetMapping("/bilibili/contents")
  public ResponseEntity<JsonNode> contents(@RequestParam Map<String, String> query) {
    return read(() -> app.catalog.contents(query));
  }

  @GetMapping("/bilibili/contents/{id}")
  public ResponseEntity<JsonNode> content(@PathVariable String id) {
    return read(() -> app.catalog.content(id));
  }

  @GetMapping("/bilibili/contents/{id}/parts")
  public ResponseEntity<JsonNode> parts(
      @PathVariable String id, @RequestParam Map<String, String> query) {
    return read(
        () -> {
          ObjectNode content = app.catalog.content(id);
          return CatalogService.page(
              list(content.path("mediaUnits")), query, text(content, "revision"));
        });
  }

  @PostMapping("/download-targets/validate")
  public ResponseEntity<JsonNode> validateTarget(
      @RequestHeader("Idempotency-Key") String key, @RequestBody JsonNode body) {
    return write("target", key, body, () -> app.downloads.validateTarget(body));
  }

  @GetMapping("/download-targets/{id}")
  public ResponseEntity<JsonNode> target(@PathVariable String id) {
    return read(() -> app.store.get("target", id));
  }

  @PostMapping("/download-previews")
  public ResponseEntity<JsonNode> preview(
      @RequestHeader("Idempotency-Key") String key, @RequestBody JsonNode body) {
    return write("preview", key, body, () -> app.downloads.preview(body));
  }

  @GetMapping("/download-previews/{id}")
  public ResponseEntity<JsonNode> getPreview(@PathVariable String id) {
    return read(() -> app.downloads.getPreview(id));
  }

  @GetMapping("/download-previews/{id}/items")
  public ResponseEntity<JsonNode> previewItems(
      @PathVariable String id, @RequestParam Map<String, String> query) {
    return read(
        () -> CatalogService.page(list(app.downloads.getPreview(id).path("items")), query, id));
  }

  @PostMapping("/download-orders")
  public ResponseEntity<JsonNode> submit(
      @RequestHeader("Idempotency-Key") String key, @RequestBody JsonNode body) {
    return write("order", key, body, () -> app.downloads.submit(body));
  }

  @GetMapping("/download-orders")
  public ResponseEntity<JsonNode> orders(@RequestParam Map<String, String> query) {
    return read(
        () -> {
          List<JsonNode> out = new ArrayList<>();
          for (JsonNode order : app.store.all("order"))
            out.add(app.downloads.order(text(order, "id")));
          return CatalogService.page(out, query, "orders");
        });
  }

  @GetMapping("/download-orders/{id}")
  public ResponseEntity<JsonNode> order(@PathVariable String id) {
    return read(() -> app.downloads.order(id));
  }

  @GetMapping("/tasks")
  public ResponseEntity<JsonNode> tasks(@RequestParam Map<String, String> query) {
    return read(
        () -> {
          app.queue.expire();
          return CatalogService.page(app.store.all("task"), query, "tasks");
        });
  }

  @GetMapping("/tasks/{id}")
  public ResponseEntity<JsonNode> task(@PathVariable String id) {
    return read(
        () -> {
          app.queue.expire();
          return app.store.get("task", id);
        });
  }

  @PostMapping("/tasks/{id}/actions")
  public ResponseEntity<JsonNode> action(
      @PathVariable String id,
      @RequestHeader("If-Match") String revision,
      @RequestBody JsonNode body) {
    return read(() -> app.queue.control(id, etag(revision), required(body, "action")));
  }

  private static String etag(String value) {
    if (value.startsWith("\"") && value.endsWith("\""))
      return value.substring(1, value.length() - 1);
    throw Failure.bad("INVALID_INPUT", "If-Match must use the quoted revision");
  }
}
