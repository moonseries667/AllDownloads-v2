package dev.alldownloads.http;

import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

import com.fasterxml.jackson.databind.JsonNode;
import dev.alldownloads.BackendApplication;
import dev.alldownloads.business.BusinessApplication;
import java.net.URI;
import java.net.http.*;
import java.nio.file.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.web.servlet.context.ServletWebServerApplicationContext;

class HttpBoundaryTest {
  @TempDir Path temp;

  @Test
  void realHttpEnvelopeIdempotencyRevisionAndRunnerSeparation() throws Exception {
    SpringApplication spring = new SpringApplication(BackendApplication.class);
    spring.setDefaultProperties(
        Map.of(
            "server.port",
            "0",
            "alldownloads.database",
            temp.resolve("business.db").toString(),
            "alldownloads.runner-token-file",
            temp.resolve("runner.token").toString(),
            "alldownloads.download-root",
            temp.resolve("downloads").toString(),
            "spring.main.banner-mode",
            "off",
            "logging.level.root",
            "ERROR"));
    try (var context = (ServletWebServerApplicationContext) spring.run()) {
      String base = "http://127.0.0.1:" + context.getWebServer().getPort();
      HttpClient client = HttpClient.newHttpClient();
      assertEquals(
          "PARTIAL",
          parse(call(client, base, "GET", "/api/v1/health", null, Map.of()).body())
              .path("data")
              .path("runtime")
              .path("state")
              .asText());
      assertEquals(
          401,
          call(
                  client,
                  base,
                  "POST",
                  "/internal/v1/leases/acquire",
                  object("runnerId", "x", "capabilities", List.of()),
                  Map.of())
              .statusCode());
      assertEquals(
          403,
          call(
                  client,
                  base,
                  "POST",
                  "/api/v1/download-previews",
                  object(),
                  Map.of("Origin", "https://untrusted.invalid"))
              .statusCode());
      String token =
          Files.readString(
              temp.resolve(
                  "runner.token")); // Test harness forwards its own generated secret; never logged
                                    // or returned.
      assertEquals(
          403,
          call(
                  client,
                  base,
                  "GET",
                  "/api/v1/health",
                  null,
                  Map.of("Authorization", "Bearer " + token))
              .statusCode());
      var registered =
          call(
              client,
              base,
              "POST",
              "/internal/v1/runners/register",
              object(
                  "runnerId",
                  "http-fixture",
                  "protocolVersion",
                  1,
                  "capabilities",
                  List.of("VALIDATE_TARGET"),
                  "tools",
                  object()),
              Map.of("Authorization", "Bearer " + token));
      assertEquals(200, registered.statusCode());
      var add =
          call(
              client,
              base,
              "POST",
              "/api/v1/bilibili/sources",
              object("links", List.of("https://www.bilibili.com/video/BV1Fixture")),
              Map.of("Idempotency-Key", "same"));
      assertEquals(202, add.statusCode());
      JsonNode data = parse(add.body()).path("data");
      assertEquals(
          data,
          parse(
                  call(
                          client,
                          base,
                          "POST",
                          "/api/v1/bilibili/sources",
                          object("links", List.of("https://www.bilibili.com/video/BV1Fixture")),
                          Map.of("Idempotency-Key", "same"))
                      .body())
              .path("data"));
      assertEquals(
          409,
          call(
                  client,
                  base,
                  "POST",
                  "/api/v1/bilibili/sources",
                  object("links", List.of("https://www.bilibili.com/video/BV2Fixture")),
                  Map.of("Idempotency-Key", "same"))
              .statusCode());
      String sourceId = data.path("items").get(0).path("sourceId").asText();
      var pinned =
          call(
              client,
              base,
              "PATCH",
              "/api/v1/bilibili/sources/" + sourceId,
              object("pinned", true),
              Map.of("If-Match", "\"1\""));
      assertEquals(200, pinned.statusCode());
      assertEquals("\"2\"", pinned.headers().firstValue("ETag").orElseThrow());
      assertEquals(
          409,
          call(
                  client,
                  base,
                  "PATCH",
                  "/api/v1/bilibili/sources/" + sourceId,
                  object("pinned", false),
                  Map.of("If-Match", "\"1\""))
              .statusCode());
      assertEquals(
          0,
          context
              .getBean(BusinessApplication.class)
              .store
              .tx(() -> context.getBean(BusinessApplication.class).store.all("order").size()));
    }
  }

  private static HttpResponse<String> call(
      HttpClient client,
      String base,
      String method,
      String path,
      JsonNode body,
      Map<String, String> headers)
      throws Exception {
    HttpRequest.Builder request =
        HttpRequest.newBuilder(URI.create(base + path))
            .header("Content-Type", "application/json")
            .timeout(java.time.Duration.ofSeconds(10));
    headers.forEach(request::header);
    return client.send(
        request
            .method(
                method,
                body == null
                    ? HttpRequest.BodyPublishers.noBody()
                    : HttpRequest.BodyPublishers.ofString(body.toString()))
            .build(),
        HttpResponse.BodyHandlers.ofString());
  }
}
