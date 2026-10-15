package dev.alldownloads.business;

import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;

class BusinessFlowTest {
  @TempDir Path temp;
  BusinessStore store;
  BusinessApplication app;
  MutableClock clock = new MutableClock();
  JsonNode examples;

  @BeforeEach
  void open() throws Exception {
    examples = MAPPER.readTree(Path.of("../../contracts/feedback1.examples.json").toFile());
    start();
    validateDefault();
    seed((ObjectNode) examples.path("content").deepCopy());
  }

  void start() throws Exception {
    store = new BusinessStore(temp.resolve("control.db"), clock);
    app = new BusinessApplication(store, temp.resolve("default").toString());
    store.tx(
        () ->
            app.queue.register(
                object(
                    "runnerId",
                    "runner",
                    "protocolVersion",
                    1,
                    "capabilities",
                    List.of(
                        "VALIDATE_TARGET",
                        "SYNC_BIL_SOURCE",
                        "FETCH_BIL_METADATA",
                        "DOWNLOAD_MEDIA"),
                    "tools",
                    object())));
  }

  @AfterEach
  void close() throws Exception {
    if (store != null) store.close();
  }

  void validateDefault() {
    JsonNode lease = acquire("VALIDATE_TARGET");
    if (lease.isNull()) return;
    complete(
        lease,
        object(
            "targetId",
            "default",
            "rootId",
            "root-default",
            "canonicalPath",
            temp.resolve("default").toString(),
            "writable",
            true));
  }

  void seed(ObjectNode content) {
    store.tx(
        () -> {
          store.put("source", "source-fixture", examples.path("source"));
          return app.catalog.importContent(content, "source-fixture");
        });
  }

  JsonNode acquire(String capability) {
    return store
        .tx(
            () ->
                app.queue.acquire(
                    object("runnerId", "runner", "capabilities", List.of(capability))))
        .path("operation");
  }

  ObjectNode result(JsonNode op, JsonNode facts) {
    return object(
        "eventId",
        id(),
        "runnerId",
        op.path("runnerId"),
        "leaseId",
        op.path("leaseId"),
        "attempt",
        op.path("attempt"),
        "fencingToken",
        op.path("fencingToken"),
        "status",
        "SUCCEEDED",
        "result",
        facts,
        "error",
        null);
  }

  ObjectNode complete(JsonNode op, JsonNode facts) {
    ObjectNode body = result(op, facts);
    store.tx(() -> app.queue.result(text(op, "operationId"), body, false));
    return body;
  }

  ObjectNode request(String... tracks) {
    ObjectNode selection =
        object(
            "mode", "EXPLICIT", "resource", "BILIBILI_CONTENTS", "ids", List.of("content-fixture"));
    if (tracks != null)
      selection.set(
          "mediaSelections",
          array(
              List.of(
                  object(
                      "contentId",
                      "content-fixture",
                      "contentRevision",
                      "fixture-v1",
                      "trackIds",
                      Arrays.asList(tracks)))));
    return object("platform", "BILIBILI", "selection", selection);
  }

  ObjectNode preview(ObjectNode request) {
    String id = store.tx(() -> app.downloads.preview(request)).path("previewId").asText();
    return store.tx(() -> app.downloads.getPreview(id));
  }

  ObjectNode submit(ObjectNode preview) {
    return store.tx(() -> app.downloads.submit(object("previewId", preview.path("id"))));
  }

  ObjectNode custom(ObjectNode request, String name) {
    ObjectNode ref =
        store.tx(() -> app.downloads.validateTarget(object("path", temp.resolve(name).toString())));
    boolean validated = false;
    for (int i = 0; i < 5 && !validated; i++) {
      JsonNode op = acquire("VALIDATE_TARGET");
      assertFalse(op.isNull());
      JsonNode payload = op.path("payload");
      complete(
          op,
          object(
              "targetId",
              payload.path("targetId"),
              "rootId",
              "root-" + Path.of(text(payload, "path")).getFileName(),
              "canonicalPath",
              payload.path("path"),
              "writable",
              true));
      validated = payload.path("targetId").equals(ref.path("targetId"));
    }
    assertTrue(validated);
    request.set("target", object("id", ref.path("targetId"), "revision", "1"));
    return request;
  }

  ObjectNode fileFacts(JsonNode op) {
    List<JsonNode> files = new ArrayList<>();
    for (JsonNode output : op.path("payload").path("outputs"))
      files.add(
          object(
              "outputId",
              output.path("id"),
              "relativePath",
              output.path("relativePath"),
              "sizeBytes",
              "12",
              "sha256",
              "a".repeat(64),
              "mediaKinds",
              text(output, "kind").equals("MUXED")
                  ? List.of("VIDEO", "AUDIO")
                  : List.of(text(output, "kind")),
              "durationSeconds",
              12,
              "verified",
              true));
    return object("files", files, "warnings", List.of());
  }

  @Test
  void allFourSortsUsePublishedTimeAndStablePaginationWithShanghaiBoundaries() {
    for (int i = 0; i < 4; i++) {
      ObjectNode c = (ObjectNode) examples.path("content").deepCopy();
      c.put("id", "c" + i);
      c.put("externalId", "BVFixture" + i);
      c.put("title", i < 2 ? "A" : "Z");
      c.put(
          "publishedAt",
          new String[] {
                "2026-10-14T15:59:59Z", "2026-10-14T16:00:00Z", "2026-10-15T15:59:59Z", null
              }
              [i]);
      seed(c);
    }
    assertEquals(
        List.of("c0", "c1", "c2", "c3", "content-fixture"),
        ids(store.tx(() -> app.catalog.contents(Map.of("sort", "TITLE_ASC")))));
    assertEquals(
        List.of("content-fixture", "c2", "c3", "c0", "c1"),
        ids(store.tx(() -> app.catalog.contents(Map.of("sort", "TITLE_DESC")))));
    assertEquals(
        List.of("c2", "c1", "content-fixture", "c0", "c3"),
        ids(store.tx(() -> app.catalog.contents(Map.of("sort", "PUBLISHED_DESC")))));
    assertEquals(
        List.of("c0", "c1", "content-fixture", "c2", "c3"),
        ids(store.tx(() -> app.catalog.contents(Map.of("sort", "PUBLISHED_ASC")))));
    assertEquals(
        List.of("c2", "c1", "content-fixture"),
        ids(
            store.tx(
                () -> app.catalog.contents(Map.of("from", "2026-10-15", "to", "2026-10-15")))));
    ObjectNode first = store.tx(() -> app.catalog.contents(Map.of("pageSize", "2")));
    String cursor = text(first, "nextCursor");
    assertEquals(
        List.of("content-fixture", "c0"),
        ids(store.tx(() -> app.catalog.contents(Map.of("pageSize", "2", "cursor", cursor)))));
    assertCode(
        "VERSION_CHANGED",
        () ->
            store.tx(
                () ->
                    app.catalog.contents(
                        Map.of("pageSize", "2", "cursor", cursor, "sort", "TITLE_ASC"))));
    seed((ObjectNode) examples.path("content").deepCopy());
    assertCode(
        "VERSION_CHANGED",
        () -> store.tx(() -> app.catalog.contents(Map.of("pageSize", "2", "cursor", cursor))));
    assertEquals(0, store.tx(() -> store.all("order").size()));
  }

  static List<String> ids(JsonNode page) {
    return list(page.path("items")).stream().map(c -> text(c, "id")).toList();
  }

  @Test
  void pinnedRevisionPersistsAndRefreshDoesNotDownload() throws Exception {
    store.tx(() -> app.catalog.sourcePatch("source-fixture", "1", object("pinned", true)));
    assertCode(
        "VERSION_CHANGED",
        () ->
            store.tx(
                () -> app.catalog.sourcePatch("source-fixture", "1", object("pinned", false))));
    ObjectNode refresh = store.tx(() -> app.catalog.refresh("source-fixture"));
    assertEquals(
        refresh.path("taskId"),
        store.tx(() -> app.catalog.refresh("source-fixture")).path("taskId"));
    assertTrue(acquire("DOWNLOAD_MEDIA").isNull());
    store.close();
    start();
    assertTrue(store.tx(() -> store.get("source", "source-fixture")).path("pinned").asBoolean());
    assertTrue(store.tx(() -> store.all("order")).isEmpty());
  }

  @Test
  void emptySelectionIsNotDefaultAndAiAndUnknownSubtitlesCannotBeSelected() {
    ObjectNode c = (ObjectNode) examples.path("content").deepCopy();
    ObjectNode ai = (ObjectNode) c.path("mediaUnits").get(0).path("tracks").get(2).deepCopy();
    ai.put("id", "ai");
    ai.put("subtitleOrigin", "AI");
    ((com.fasterxml.jackson.databind.node.ArrayNode) c.path("mediaUnits").get(0).path("tracks"))
        .add(ai);
    ObjectNode unknown = ai.deepCopy();
    unknown.put("id", "unknown");
    unknown.put("subtitleOrigin", "UNKNOWN");
    ((com.fasterxml.jackson.databind.node.ArrayNode) c.path("mediaUnits").get(0).path("tracks"))
        .add(unknown);
    seed(c);
    assertCode("EMPTY_SELECTION", () -> preview(request()));
    assertCode("INVALID_INPUT", () -> preview(request("ai")));
    assertCode("INVALID_INPUT", () -> preview(request("unknown")));
    ObjectNode all = preview(request((String[]) null));
    JsonNode item = all.path("items").get(0);
    assertEquals(4, item.path("selectedTrackIds").size());
    assertEquals(3, item.path("outputs").size());
    assertEquals("MUX", text(item, "mergePolicy"));
    assertTrue(item.path("sizeBytes").isNull());
    assertEquals(
        "AUDIO",
        text(
            preview(request("audio-fixture")).path("items").get(0).path("outputs").get(0), "kind"));
    assertEquals(
        "SUBTITLE",
        text(
            preview(request("subtitle-fixture")).path("items").get(0).path("outputs").get(0),
            "kind"));
  }

  @Test
  void resourceAndTargetChangesInvalidatePreviewAndSubmittedSnapshotRemainsFixed() {
    ObjectNode preview = preview(request("video-fixture"));
    ObjectNode updated = (ObjectNode) examples.path("content").deepCopy();
    updated.put("revision", "v2");
    seed(updated);
    assertCode("VERSION_CHANGED", () -> submit(preview));
    seed((ObjectNode) examples.path("content").deepCopy());
    ObjectNode next = preview(request("video-fixture"));
    ObjectNode submitted = submit(next);
    ObjectNode original = store.tx(() -> app.downloads.order(text(submitted, "orderId")));
    store.tx(
        () -> {
          app.downloads.configureDefault(temp.resolve("new-default").toString());
          return null;
        });
    assertEquals(
        original.path("target"),
        store.tx(() -> app.downloads.order(text(submitted, "orderId"))).path("target"));
    assertEquals(
        next.path("target").path("path"),
        acquire("DOWNLOAD_MEDIA").path("destination").path("rootPath"));
  }

  @Test
  void targetRevisionAndExpiryRejectUnsubmittedPreview() {
    ObjectNode p = preview(request("video-fixture"));
    store.tx(
        () -> {
          app.downloads.configureDefault(temp.resolve("changed").toString());
          return null;
        });
    assertCode("STORAGE_CHANGED", () -> submit(p));
    ObjectNode custom = preview(custom(request("audio-fixture"), "alternate"));
    clock.now = clock.now.plusSeconds(601);
    assertCode("PREVIEW_EXPIRED", () -> submit(custom));
  }

  @Test
  void exactOutputCoverageDoesNotTurnAudioOnlyIntoWholeContentReuse() {
    submit(preview(request("audio-fixture")));
    JsonNode audio = acquire("DOWNLOAD_MEDIA");
    complete(audio, fileFacts(audio));
    ObjectNode audioAgain = preview(request("audio-fixture"));
    assertEquals("LOCAL_VERIFIED", text(audioAgain.path("items").get(0), "reasonCode"));
    ObjectNode video = preview(request("video-fixture"));
    assertEquals("DOWNLOAD", text(video.path("items").get(0), "decision"));
    ObjectNode all = preview(request((String[]) null));
    assertEquals("DOWNLOAD", text(all.path("items").get(0), "decision"));
    assertEquals(
        "PARTIAL",
        text(
            store.tx(() -> app.catalog.content("content-fixture")).path("downloadSummary"),
            "state"));
  }

  @Test
  void customTargetStaysUnarchivedAcrossRetryRestartAndReplay() throws Exception {
    ObjectNode p = preview(custom(request("video-fixture", "audio-fixture"), "custom"));
    ObjectNode order = submit(p);
    JsonNode op = acquire("DOWNLOAD_MEDIA");
    ObjectNode failure = result(op, null);
    failure.put("status", "RETRYABLE_ERROR");
    failure.set(
        "error",
        object(
            "code",
            "NETWORK_TEMPORARY",
            "message",
            "transient",
            "retryAfterSeconds",
            0,
            "completedEffects",
            List.of(),
            "pendingEffects",
            List.of()));
    store.tx(() -> app.queue.result(text(op, "operationId"), failure, false));
    clock.now = clock.now.plusSeconds(31);
    store.close();
    start();
    JsonNode retry = acquire("DOWNLOAD_MEDIA");
    assertEquals(text(op, "operationId"), text(retry, "operationId"));
    assertEquals("NONE", text(retry.path("payload"), "archivePolicy"));
    assertEquals(temp.resolve("custom").toString(), text(retry.path("destination"), "rootPath"));
    ObjectNode result = result(retry, fileFacts(retry));
    ObjectNode ack = store.tx(() -> app.queue.result(text(retry, "operationId"), result, false));
    store.close();
    start();
    assertEquals(ack, store.tx(() -> app.queue.result(text(retry, "operationId"), result, false)));
    assertEquals(
        "SUCCEEDED", text(store.tx(() -> app.downloads.order(text(order, "orderId"))), "status"));
    assertTrue(
        store
            .tx(() -> store.rows("SELECT id FROM operations WHERE type='UPLOAD_ARTIFACT'"))
            .isEmpty());
    assertTrue(
        store.tx(() -> store.all("file")).stream()
            .allMatch(
                f ->
                    text(f, "archivePolicy").equals("NONE")
                        && text(f, "archiveState").equals("NOT_REQUESTED")));
    ObjectNode defaultPreview = preview(request("video-fixture", "audio-fixture"));
    assertEquals("AUTO", text(defaultPreview, "archivePolicy"));
    assertEquals("DOWNLOAD", text(defaultPreview.path("items").get(0), "decision"));
  }

  @Test
  void leaseExpiryCannotReexecuteAndConflictingResultCannotCommitFiles() {
    submit(preview(request("video-fixture")));
    JsonNode op = acquire("DOWNLOAD_MEDIA");
    ObjectNode invalid = fileFacts(op);
    ((ObjectNode) invalid.path("files").get(0)).put("relativePath", "wrong.mp4");
    assertCode("VERIFICATION_FAILED", () -> complete(op, invalid));
    assertTrue(store.tx(() -> store.all("file")).isEmpty());
    clock.now = clock.now.plusSeconds(91);
    assertTrue(acquire("DOWNLOAD_MEDIA").isNull());
    ObjectNode result = result(op, fileFacts(op));
    assertCode(
        "RECONCILE_REQUIRED",
        () -> store.tx(() -> app.queue.result(text(op, "operationId"), result, false)));
    store.tx(() -> app.queue.result(text(op, "operationId"), result, true));
    ObjectNode changed = result.deepCopy();
    changed.put("status", "CANCELLED");
    assertCode(
        "EVENT_CONFLICT",
        () -> store.tx(() -> app.queue.result(text(op, "operationId"), changed, false)));
  }

  @Test
  void pathsHandleSingleMultipartSeasonsMovieLanguageAndUnsafeNamesWithoutMoving() {
    String single =
        preview(request("video-fixture")).path("items").get(0).path("relativePath").asText();
    assertEquals("Bilibili/影像收藏(收藏夹)/隔离验证示例(BV1Fixture)", single);
    ObjectNode renamed = (ObjectNode) examples.path("content").deepCopy();
    renamed.put("title", "new title");
    seed(renamed);
    assertEquals(
        single,
        preview(request("video-fixture")).path("items").get(0).path("relativePath").asText());
    ObjectNode multi = (ObjectNode) examples.path("content").deepCopy();
    multi.put("kind", "MULTIPART");
    multi.put("id", "multi");
    multi.put("externalId", "BVmulti");
    ((ObjectNode) multi.path("mediaUnits").get(0)).put("kind", "PART");
    seed(multi);
    ObjectNode series = (ObjectNode) examples.path("content").deepCopy();
    series.put("kind", "SERIES");
    series.put("externalIdType", "SEASON_ID");
    series.put("id", "season1");
    series.put("externalId", "123");
    series.put("seasonId", "123");
    ((ObjectNode) series.path("mediaUnits").get(0)).put("kind", "EPISODE");
    seed(series);
    ObjectNode s2 = series.deepCopy();
    s2.put("id", "season2");
    s2.put("externalId", "124");
    s2.put("seasonId", "124");
    seed(s2);
    store.tx(
        () -> {
          PathPlanner paths = new PathPlanner(store);
          ObjectNode mc = app.catalog.content("multi");
          assertTrue(paths.unit(mc, mc.path("mediaUnits").get(0)).endsWith("/P01"));
          ObjectNode s1 = app.catalog.content("season1"), ss2 = app.catalog.content("season2");
          assertNotEquals(paths.content(s1), paths.content(ss2));
          assertTrue(paths.unit(s1, s1.path("mediaUnits").get(0)).contains("第01集("));
          return null;
        });
    assertEquals("_CON", PathPlanner.safe("CON"));
    assertEquals("a_b_c", PathPlanner.safe("a:b?c"));
    assertTrue(PathPlanner.safe("x".repeat(500)).length() < 66);
  }

  @Test
  void overviewNeverEquatesMountDirectoryWithArchiveAndLostRunnerIsVisible() {
    ObjectNode overview = store.tx(app::overview);
    assertEquals("UNKNOWN", text(overview.path("runtime").path("mount"), "state"));
    assertEquals(0, overview.path("bilibili").path("archiveCount").asInt());
    clock.now = clock.now.plusSeconds(91);
    assertEquals("PARTIAL", text(store.tx(app::overview).path("runtime"), "state"));
  }

  @Test
  void overlappingSelectionsShareOnlyTheSameOutputAndHaveOneWriter() {
    ObjectNode first = submit(preview(request("audio-fixture")));
    ObjectNode second = submit(preview(request("audio-fixture", "subtitle-fixture")));
    assertEquals(2, second.path("taskIds").size());
    assertTrue(strings(second.path("taskIds")).contains(first.path("taskIds").get(0).asText()));
    store.tx(
        () -> {
          List<String[]> ops =
              store.rows("SELECT body FROM operations WHERE type='DOWNLOAD_MEDIA'");
          assertEquals(2, ops.size());
          Set<String> paths = new HashSet<>();
          for (String[] op : ops)
            for (JsonNode output : parse(op[0]).path("payload").path("outputs"))
              assertTrue(paths.add(text(output, "relativePath")));
          return null;
        });
  }

  @Test
  void failureReasonsAndArchiveFactsRemainDistinct() {
    ObjectNode removed = (ObjectNode) examples.path("content").deepCopy();
    removed.put("availability", "REMOVED");
    removed.put("availabilityReason", "RESOURCE_DELETED");
    seed(removed);
    assertEquals(
        "RESOURCE_DELETED",
        text(preview(request((String[]) null)).path("items").get(0), "reasonCode"));
    removed.put("availabilityReason", "NETWORK_TEMPORARY");
    seed(removed);
    assertNotEquals(
        "RESOURCE_DELETED",
        text(preview(request((String[]) null)).path("items").get(0), "reasonCode"));
    removed.put("availability", "UNAVAILABLE");
    removed.put("availabilityReason", "PERMISSION_DENIED");
    seed(removed);
    ObjectNode denied = preview(request((String[]) null));
    assertEquals("PERMISSION_DENIED", text(denied.path("items").get(0), "reasonCode"));
    assertCode("EMPTY_SELECTION", () -> submit(denied));
    seed((ObjectNode) examples.path("content").deepCopy());
    submit(preview(request("audio-fixture")));
    JsonNode op = acquire("DOWNLOAD_MEDIA");
    complete(op, fileFacts(op));
    JsonNode item = preview(request("audio-fixture")).path("items").get(0);
    assertEquals("REUSE", text(item, "decision"));
    assertEquals("LOCAL_VERIFIED", text(item, "reasonCode"));
    assertEquals("NOT_REQUESTED", text(item, "archiveState"));
  }

  @Test
  void movieLanguagesAndCaseInsensitiveCollisionsKeepUniqueStablePaths() {
    store.tx(
        () -> {
          PathPlanner planner = new PathPlanner(store);
          ObjectNode movie = (ObjectNode) examples.path("content").deepCopy();
          movie.put("id", "movie");
          movie.put("kind", "MOVIE");
          movie.put("externalId", "123");
          movie.put("externalIdType", "MEDIA_ID");
          ObjectNode zh = (ObjectNode) movie.path("mediaUnits").get(0).deepCopy();
          zh.put("id", "zh");
          zh.put("kind", "LANGUAGE");
          zh.put("title", "普通话");
          ObjectNode en = zh.deepCopy();
          en.put("id", "en");
          en.put("title", "英语");
          movie.set("mediaUnits", array(List.of(zh, en)));
          assertTrue(planner.unit(movie, zh).endsWith("/普通话"));
          assertTrue(planner.unit(movie, en).endsWith("/英语"));
          assertFalse(planner.unit(movie, en).contains("P01"));
          String a = planner.assign("a", "Bilibili/ABC"), b = planner.assign("b", "Bilibili/abc");
          assertNotEquals(a.toLowerCase(Locale.ROOT), b.toLowerCase(Locale.ROOT));
          assertEquals(a, planner.assign("a", "Bilibili/renamed"));
          return null;
        });
  }

  @Test
  void capturePublicAndRunnerSnapshotsForSharedSchemaValidation() throws Exception {
    ObjectNode preview = preview(request((String[]) null));
    submit(preview);
    JsonNode operation = acquire("DOWNLOAD_MEDIA");
    List<JsonNode> captures =
        store.tx(
            () ->
                List.of(
                    object(
                        "schema",
                        "Source",
                        "data",
                        app.catalog.publicSource(store.get("source", "source-fixture"))),
                    object("schema", "Content", "data", app.catalog.content("content-fixture")),
                    object("schema", "Preview", "data", preview),
                    object("schema", "Target", "data", store.get("target", "default")),
                    object("schema", "Overview", "data", app.overview())));
    Files.writeString(Path.of("target/contract-captures.json"), array(captures).toPrettyString());
    Files.writeString(Path.of("target/runner-capture.json"), operation.toPrettyString());
  }

  @Test
  void stoppedDownloadPreservesPartialFactsAndRetriesOnlyItsFrozenOperation() {
    ObjectNode p = preview(custom(request("audio-fixture", "subtitle-fixture"), "partial"));
    ObjectNode submitted = submit(p);
    JsonNode op = acquire("DOWNLOAD_MEDIA");
    ObjectNode facts = fileFacts(op);
    ((com.fasterxml.jackson.databind.node.ArrayNode) facts.path("files")).remove(1);
    ObjectNode failed = result(op, facts);
    failed.put("status", "RETRYABLE_ERROR");
    failed.set(
        "error",
        object(
            "code",
            "NETWORK_INTERRUPTED",
            "message",
            "interrupted",
            "completedEffects",
            List.of(facts.path("files").get(0).path("outputId").asText()),
            "pendingEffects",
            List.of("unfinished frozen outputs")));
    store.tx(() -> app.queue.result(text(op, "operationId"), failed, false));
    assertEquals(1, store.tx(() -> store.all("file").size()));
    assertTrue(
        store
            .tx(() -> store.rows("SELECT id FROM operations WHERE type='UPLOAD_ARTIFACT'"))
            .isEmpty());
    ObjectNode refresh = preview(customRequestFor(p, "audio-fixture", "subtitle-fixture"));
    assertEquals("NETWORK_TEMPORARY", text(refresh.path("items").get(0), "reasonCode"));
    assertEquals(submitted.path("taskIds"), submit(refresh).path("taskIds"));
    clock.now = clock.now.plusSeconds(31);
    JsonNode retry = acquire("DOWNLOAD_MEDIA");
    assertEquals(op.path("operationId"), retry.path("operationId"));
    assertEquals(op.path("payload"), retry.path("payload"));
    complete(retry, fileFacts(retry));
    assertEquals(2, store.tx(() -> store.all("file").size()));
    assertTrue(
        store
            .tx(() -> store.rows("SELECT id FROM operations WHERE type='UPLOAD_ARTIFACT'"))
            .isEmpty());
  }

  ObjectNode customRequestFor(JsonNode p, String... tracks) {
    ObjectNode request = request(tracks);
    request.set(
        "target",
        object("id", p.path("target").path("id"), "revision", p.path("target").path("revision")));
    return request;
  }

  @Test
  void cancellationRetainsVerifiedFilesWithoutSchedulingArchive() {
    submit(preview(request("audio-fixture", "subtitle-fixture")));
    JsonNode op = acquire("DOWNLOAD_MEDIA");
    store.tx(
        () ->
            app.queue.control(
                text(op, "taskId"),
                text(store.get("task", text(op, "taskId")), "revision"),
                "CANCEL"));
    ObjectNode facts = fileFacts(op);
    ((com.fasterxml.jackson.databind.node.ArrayNode) facts.path("files")).remove(1);
    ObjectNode cancelled = result(op, facts);
    cancelled.put("status", "CANCELLED");
    store.tx(() -> app.queue.result(text(op, "operationId"), cancelled, false));
    assertEquals(1, store.tx(() -> store.all("file").size()));
    assertEquals(
        "CANCELLED", text(store.tx(() -> store.get("task", text(op, "taskId"))), "status"));
    assertTrue(
        store
            .tx(() -> store.rows("SELECT id FROM operations WHERE type='UPLOAD_ARTIFACT'"))
            .isEmpty());
  }

  @Test
  void recentSourceRefreshReusesInformationAndForcedRefreshStillNeverDownloads() {
    ObjectNode source =
        store.tx(
            () ->
                app.catalog.addSources(
                    object("links", List.of("https://www.bilibili.com/video/BVFixture"))));
    String sourceId = source.path("items").get(0).path("sourceId").asText();
    JsonNode op = acquire("SYNC_BIL_SOURCE");
    complete(
        op,
        object(
            "source",
            object("title", "示例"),
            "contents",
            List.of(examples.path("content")),
            "complete",
            true));
    assertEquals(
        op.path("taskId"), store.tx(() -> app.catalog.refresh(sourceId, false)).path("taskId"));
    assertTrue(acquire("SYNC_BIL_SOURCE").isNull());
    store.tx(() -> app.catalog.refresh(sourceId, true));
    assertFalse(acquire("SYNC_BIL_SOURCE").isNull());
    assertTrue(acquire("DOWNLOAD_MEDIA").isNull());
  }

  @Test
  void confirmedMetadataDeletionInvalidatesAnOlderPreviewWithoutCreatingDownloads() {
    ObjectNode added =
        store.tx(
            () ->
                app.catalog.addSources(
                    object("links", List.of("https://www.bilibili.com/video/BV1Fixture"))));
    String sourceId = added.path("items").get(0).path("sourceId").asText();
    JsonNode sync = acquire("SYNC_BIL_SOURCE");
    complete(
        sync,
        object(
            "source",
            object("title", "示例"),
            "contents",
            List.of(examples.path("content")),
            "complete",
            true));
    ObjectNode oldPreview = preview(request("audio-fixture"));
    store.tx(() -> app.catalog.refresh(sourceId, true));
    JsonNode refresh = acquire("SYNC_BIL_SOURCE");
    ObjectNode failure = result(refresh, null);
    failure.put("status", "NEEDS_ACTION");
    failure.set(
        "error",
        object(
            "code",
            "RESOURCE_DELETED",
            "message",
            "confirmed",
            "completedEffects",
            List.of(),
            "pendingEffects",
            List.of()));
    store.tx(() -> app.queue.result(text(refresh, "operationId"), failure, false));
    assertCode("VERSION_CHANGED", () -> submit(oldPreview));
    assertEquals(
        "RESOURCE_DELETED",
        text(preview(request((String[]) null)).path("items").get(0), "reasonCode"));
    assertTrue(acquire("DOWNLOAD_MEDIA").isNull());
  }

  static void assertCode(String code, Runnable action) {
    assertEquals(code, assertThrows(Failure.class, action::run).code);
  }

  static final class MutableClock extends Clock {
    Instant now = Instant.parse("2026-10-15T10:00:00Z");

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId z) {
      return this;
    }

    public Instant instant() {
      return now;
    }
  }
}
