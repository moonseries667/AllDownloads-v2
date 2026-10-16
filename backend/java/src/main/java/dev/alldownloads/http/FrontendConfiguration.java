package dev.alldownloads.http;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.io.Resource;
import org.springframework.http.CacheControl;
import org.springframework.web.servlet.config.annotation.ResourceHandlerRegistry;
import org.springframework.web.servlet.config.annotation.ViewControllerRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;
import org.springframework.web.servlet.resource.PathResourceResolver;

/** Serves an explicitly configured production build on the existing Java HTTP listener. */
@Configuration
public class FrontendConfiguration implements WebMvcConfigurer {
  private final Path root;

  public FrontendConfiguration(@Value("${alldownloads.frontend-root:}") String frontendRoot) {
    if (frontendRoot.isBlank()) {
      root = null;
      return;
    }
    try {
      root = Path.of(frontendRoot).toRealPath();
      if (!Files.isDirectory(root))
        throw new IllegalArgumentException("alldownloads.frontend-root must be an existing directory");
    } catch (IOException e) {
      throw new IllegalArgumentException("alldownloads.frontend-root must be an existing directory", e);
    }
  }

  @Override
  public void addViewControllers(ViewControllerRegistry registry) {
    if (root != null) registry.addViewController("/").setViewName("forward:/index.html");
  }

  @Override
  public void addResourceHandlers(ResourceHandlerRegistry registry) {
    if (root == null) return;

    // Vite emits versioned files here. A bounded cache also handles any unversioned assets.
    String assetsLocation = root.resolve("assets").toUri().toString();
    registry
        .addResourceHandler("/assets/**")
        .addResourceLocations(assetsLocation.endsWith("/") ? assetsLocation : assetsLocation + "/")
        .setCacheControl(CacheControl.maxAge(Duration.ofHours(1)).cachePublic())
        .resourceChain(false)
        .addResolver(new FrontendResourceResolver(root, false));
    // HTML and files outside assets are revalidated, so a new build is visible immediately.
    registry
        .addResourceHandler("/**")
        .addResourceLocations(root.toUri().toString())
        .setCacheControl(CacheControl.noCache())
        .resourceChain(false)
        .addResolver(new FrontendResourceResolver(root, true));
  }

  private static final class FrontendResourceResolver extends PathResourceResolver {
    private final Path root;
    private final boolean spaFallback;

    private FrontendResourceResolver(Path root, boolean spaFallback) {
      this.root = root;
      this.spaFallback = spaFallback;
    }

    @Override
    protected Resource getResource(String resourcePath, Resource location) throws IOException {
      if (spaFallback && isReservedPath(resourcePath)) return null;
      Resource resource = super.getResource(resourcePath, location);
      if (isFileInsideRoot(resource)) return resource;
      String filename = resourcePath.substring(resourcePath.lastIndexOf('/') + 1);
      if (!spaFallback || filename.contains(".")) return null;
      Resource index = super.getResource("index.html", location);
      return isFileInsideRoot(index) ? index : null;
    }

    private boolean isFileInsideRoot(Resource resource) throws IOException {
      if (resource == null) return false;
      Path path = resource.getFile().toPath().toRealPath();
      return path.startsWith(root) && Files.isRegularFile(path);
    }

    private static boolean isReservedPath(String path) {
      String relative = path.startsWith("/") ? path.substring(1) : path;
      for (String reserved : new String[] {"api", "internal", "error", "actuator", "assets"}) {
        if (relative.equals(reserved) || relative.startsWith(reserved + "/")) return true;
      }
      return false;
    }
  }
}
