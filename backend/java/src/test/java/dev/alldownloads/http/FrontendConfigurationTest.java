package dev.alldownloads.http;

import static org.junit.jupiter.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import dev.alldownloads.BackendApplication;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.web.servlet.context.ServletWebServerApplicationContext;
import org.springframework.context.annotation.Bean;
import org.springframework.core.env.MapPropertySource;
import org.springframework.http.MediaType;
import org.springframework.mock.web.MockServletContext;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.context.support.AnnotationConfigWebApplicationContext;
import org.springframework.web.servlet.config.annotation.EnableWebMvc;

class FrontendConfigurationTest {
  @TempDir Path temp;

  @Test
  void servesProductionFilesHeadAndConditionalRequestsWithSpaDeepLinks() throws Exception {
    Path dist = createDist();
    try (var context = context(dist.toString())) {
      MockMvc mvc = MockMvcBuilders.webAppContextSetup(context).build();
      mvc.perform(get("/"))
          .andExpect(status().isOk())
          .andExpect(forwardedUrl("/index.html"));
      mvc.perform(get("/index.html"))
          .andExpect(status().isOk())
          .andExpect(content().contentTypeCompatibleWith(MediaType.TEXT_HTML))
          .andExpect(header().string("Cache-Control", "no-cache"))
          .andExpect(content().string("<html>production build</html>"));
      mvc.perform(get("/bilibili/sources/fixture"))
          .andExpect(status().isOk())
          .andExpect(content().string("<html>production build</html>"));
      mvc.perform(head("/bilibili/sources/fixture"))
          .andExpect(status().isOk())
          .andExpect(content().contentTypeCompatibleWith(MediaType.TEXT_HTML))
          .andExpect(header().longValue("Content-Length", Files.size(dist.resolve("index.html"))))
          .andExpect(content().string(""));
      var asset =
          mvc.perform(get("/assets/app-12345678.js"))
              .andExpect(status().isOk())
              .andExpect(content().contentTypeCompatibleWith("text/javascript"))
              .andExpect(header().string("Cache-Control", "max-age=3600, public"))
              .andExpect(content().string("console.log('build');"))
              .andReturn();
      mvc.perform(
              get("/assets/app-12345678.js")
                  .header("If-Modified-Since", asset.getResponse().getHeader("Last-Modified")))
          .andExpect(status().isNotModified())
          .andExpect(content().string(""));
      mvc.perform(get("/assets/app-12345678.css"))
          .andExpect(status().isOk())
          .andExpect(content().contentTypeCompatibleWith("text/css"));
      mvc.perform(get("/favicon.svg"))
          .andExpect(status().isOk())
          .andExpect(content().contentTypeCompatibleWith("image/svg+xml"));
    }
  }

  @Test
  void forwardsTheRootThroughTheRealServletContainerAlongsideTheApi() throws Exception {
    SpringApplication spring = new SpringApplication(BackendApplication.class);
    spring.setDefaultProperties(
        Map.of(
            "server.port", "0",
            "alldownloads.frontend-root", createDist().toString(),
            "alldownloads.database", temp.resolve("business.db").toString(),
            "alldownloads.runner-token-file", temp.resolve("runner.token").toString(),
            "spring.main.banner-mode", "off",
            "logging.level.root", "ERROR"));
    try (var context = (ServletWebServerApplicationContext) spring.run()) {
      String base = "http://127.0.0.1:" + context.getWebServer().getPort();
      HttpClient client = HttpClient.newHttpClient();
      var page =
          client.send(
              HttpRequest.newBuilder(URI.create(base + "/")).GET().build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, page.statusCode());
      assertEquals("<html>production build</html>", page.body());
      assertEquals("no-cache", page.headers().firstValue("Cache-Control").orElseThrow());
      var head =
          client.send(
              HttpRequest.newBuilder(URI.create(base + "/"))
                  .method("HEAD", HttpRequest.BodyPublishers.noBody())
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, head.statusCode());
      assertEquals("", head.body());
      var api =
          client.send(
              HttpRequest.newBuilder(URI.create(base + "/api/v1/health")).GET().build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, api.statusCode());
      assertTrue(api.headers().firstValue("Content-Type").orElseThrow().contains("application/json"));
    }
  }

  @Test
  void missingResourcesReservedRoutesAndTraversalNeverReceiveTheSpa() throws Exception {
    Path dist = createDist();
    Files.writeString(temp.resolve("outside.txt"), "outside configured root");
    Files.createDirectories(dist.resolve("api"));
    Files.writeString(dist.resolve("api").resolve("leak.txt"), "must not be public");
    try (var context = context(dist.toString())) {
      MockMvc mvc = MockMvcBuilders.webAppContextSetup(context).build();
      for (String path :
          new String[] {
            "/assets/missing.js",
            "/assets/missing",
            "/missing.css",
            "/nested/missing.json",
            "/api",
            "/api/unknown",
            "/api/leak.txt",
            "/internal/unknown",
            "/error",
            "/error/nested",
            "/actuator/health",
            "/../outside.txt",
            "/assets/../../outside.txt",
            "/%2e%2e/outside.txt"
          }) {
        mvc.perform(get(path)).andExpect(status().isNotFound()).andExpect(content().string(""));
      }
      mvc.perform(post("/bilibili/sources/fixture")).andExpect(status().isMethodNotAllowed());
    }
  }

  @Test
  void keepsApiControllersAndTheirExistingBoundaryFilter() throws Exception {
    try (var context = context(createDist().toString())) {
      MockMvc mvc =
          MockMvcBuilders.webAppContextSetup(context)
              .addFilter(new BoundaryFilter(temp.resolve("runner.token").toString(), ""))
              .build();
      mvc.perform(get("/api/fixture"))
          .andExpect(status().isOk())
          .andExpect(content().string("api response"));
      mvc.perform(get("/api/fixture").header("Origin", "https://untrusted.invalid"))
          .andExpect(status().isForbidden());
      mvc.perform(get("/internal/unknown")).andExpect(status().isUnauthorized());
      mvc.perform(get("/bilibili"))
          .andExpect(status().isOk())
          .andExpect(content().string("<html>production build</html>"));
    }
  }

  @Test
  void leavesStaticServingDisabledByDefaultAndRejectsAnInvalidConfiguredRoot() throws Exception {
    try (var context = context("")) {
      MockMvc mvc = MockMvcBuilders.webAppContextSetup(context).build();
      mvc.perform(get("/")).andExpect(status().isNotFound());
      mvc.perform(get("/bilibili")).andExpect(status().isNotFound());
      mvc.perform(get("/api/fixture")).andExpect(status().isOk());
    }
    assertThrows(
        IllegalArgumentException.class,
        () -> new FrontendConfiguration(temp.resolve("not-created").toString()));
    Path file = Files.writeString(temp.resolve("not-a-directory"), "file");
    assertThrows(IllegalArgumentException.class, () -> new FrontendConfiguration(file.toString()));
  }

  private Path createDist() throws Exception {
    Path dist = Files.createDirectory(temp.resolve("dist"));
    Files.writeString(dist.resolve("index.html"), "<html>production build</html>");
    Files.writeString(dist.resolve("favicon.svg"), "<svg xmlns='http://www.w3.org/2000/svg'/>");
    Path assets = Files.createDirectory(dist.resolve("assets"));
    Files.writeString(assets.resolve("app-12345678.js"), "console.log('build');");
    Files.writeString(assets.resolve("app-12345678.css"), "body { color: black; }");
    return dist;
  }

  private AnnotationConfigWebApplicationContext context(String root) {
    var context = new AnnotationConfigWebApplicationContext();
    context.setServletContext(new MockServletContext());
    context
        .getEnvironment()
        .getPropertySources()
        .addFirst(new MapPropertySource("frontend-test", Map.of("alldownloads.frontend-root", root)));
    context.register(TestWebConfiguration.class, FrontendConfiguration.class);
    context.refresh();
    return context;
  }

  @EnableWebMvc
  static class TestWebConfiguration {
    @Bean
    FixtureController fixtureController() {
      return new FixtureController();
    }

    // Non-static so application component scanning cannot discover this test-only controller.
    @RestController
    class FixtureController {
      @GetMapping("/api/fixture")
      String fixture() {
        return "api response";
      }
    }
  }
}
