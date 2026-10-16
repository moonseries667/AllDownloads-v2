package dev.alldownloads.business;

import static dev.alldownloads.business.Json.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.sql.SQLException;
import java.time.*;
import java.util.*;
import java.util.regex.*;

public final class CatalogService {
  private final BusinessStore store;
  private final OperationQueue queue;
  private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");

  public CatalogService(BusinessStore store, OperationQueue queue) {
    this.store = store;
    this.queue = queue;
  }

  public ObjectNode sourcePatch(String id, String revision, JsonNode request) throws SQLException {
    ObjectNode source = store.get("source", id);
    if (!text(source, "revision").equals(revision)) throw Failure.conflict("VERSION_CHANGED");
    if (request.has("pinned")) {
      if (!request.path("pinned").isBoolean())
        throw Failure.bad("INVALID_INPUT", "pinned must be boolean");
      source.set("pinned", request.path("pinned"));
    }
    if (request.has("alias")) {
      if (!request.path("alias").isNull() && !request.path("alias").isTextual())
        throw Failure.bad("INVALID_INPUT", "alias must be text");
      source.set("alias", request.path("alias"));
    }
    source.put("revision", Long.toString(Long.parseLong(revision) + 1));
    decorateSource(source);
    store.put("source", id, source);
    return source;
  }

  public ObjectNode addSources(JsonNode request) throws SQLException {
    List<String> links = strings(request.path("links"));
    if (links.isEmpty() || links.size() > 200)
      throw Failure.bad("INVALID_INPUT", "Provide 1–200 links");
    List<JsonNode> results = new ArrayList<>();
    for (String link : new LinkedHashSet<>(links)) {
      try {
        URI uri = URI.create(link);
        String host = uri.getHost();
        if (!Set.of("http", "https").contains(uri.getScheme())
            || host == null
            || !(host.equals("bilibili.com")
                || host.endsWith(".bilibili.com")
                || host.equals("b23.tv"))
            || uri.getUserInfo() != null) throw new IllegalArgumentException();
        String kind = "USER_ADDED", externalId = link;
        Matcher bv = Pattern.compile("/(BV[A-Za-z0-9]+)(?:/|$)").matcher(uri.getPath()),
            season = Pattern.compile("/(ss|ep|md)([0-9]+)").matcher(uri.getPath());
        if (bv.find()) externalId = bv.group(1);
        else if (season.find()) {
          kind = "SERIES";
          externalId =
              season.group(1).equals("ss") ? season.group(2) : season.group(1) + season.group(2);
        } else if (link.contains("fid=") || uri.getPath().contains("/ml")) {
          kind = "FAVORITE";
          Matcher m = Pattern.compile("(?:fid=|/ml)([0-9]+)").matcher(link);
          if (m.find()) externalId = m.group(1);
        } else if (link.contains("sid=") || uri.getPath().contains("/lists/")) {
          kind = "COLLECTION";
          Matcher m = Pattern.compile("(?:sid=|/lists/)([0-9]+)").matcher(link);
          if (m.find()) externalId = m.group(1);
        }
        String sourceId =
            "bil-source-" + PathPlanner.digest(kind + ":" + externalId).substring(0, 24);
        ObjectNode old = store.optional("source", sourceId);
        boolean reused = old != null;
        if (old == null) {
          old =
              object(
                  "id",
                  sourceId,
                  "platform",
                  "BILIBILI",
                  "kind",
                  kind,
                  "title",
                  kind.equals("USER_ADDED") ? "用户添加视频" : externalId,
                  "alias",
                  null,
                  "url",
                  link,
                  "status",
                  "REFRESHING",
                  "lastSyncedAt",
                  null,
                  "itemCount",
                  0,
                  "revision",
                  "1",
                  "pinned",
                  false,
                  "externalId",
                  externalId);
          decorateSource(old);
          store.put("source", sourceId, old);
        }
        String task = refresh(sourceId, false).path("taskId").asText();
        results.add(object("sourceId", sourceId, "taskId", task, "reused", reused, "error", null));
      } catch (IllegalArgumentException e) {
        results.add(
            object(
                "sourceId",
                null,
                "taskId",
                null,
                "reused",
                false,
                "error",
                object("code", "INVALID_INPUT", "message", "无法识别此 Bilibili 链接")));
      }
    }
    return object("batchId", id(), "items", results);
  }

  public ObjectNode refresh(String sourceId) throws SQLException {
    return refresh(sourceId, true);
  }

  public ObjectNode refresh(String sourceId, boolean force) throws SQLException {
    ObjectNode source = store.get("source", sourceId);
    String[] old =
        store.row(
            "SELECT task_id FROM operations WHERE type='SYNC_BIL_SOURCE' AND resource_key=? AND"
                + " status NOT IN ('SUCCEEDED','CANCELLED','FAILED')",
            "source:" + sourceId);
    if (!force
        && source.path("lastSyncedAt").isTextual()
        && Instant.parse(text(source, "lastSyncedAt"))
            .isAfter(store.clock.instant().minusSeconds(300))) {
      String[] completed =
          store.row(
              "SELECT task_id FROM operations WHERE type='SYNC_BIL_SOURCE' AND resource_key=? AND"
                  + " status='SUCCEEDED' ORDER BY created DESC LIMIT 1",
              "source:" + sourceId);
      if (completed != null) return object("taskId", completed[0], "reused", true);
    }
    String task =
        queue.enqueue(
            "SYNC_BIL_SOURCE",
            "BILIBILI",
            "source:" + sourceId,
            null,
            "1",
            object(
                "schemaVersion",
                1,
                "sourceId",
                sourceId,
                "kind",
                source.path("kind"),
                "externalId",
                source.path("externalId"),
                "url",
                source.path("url")));
    source.put("status", "REFRESHING");
    store.put("source", sourceId, source);
    return object("taskId", task, "reused", old != null);
  }

  public List<JsonNode> sources() throws SQLException {
    List<JsonNode> all = store.all("source");
    all.sort(
        Comparator.<JsonNode, Boolean>comparing(s -> s.path("pinned").asBoolean())
            .reversed()
            .thenComparing(s -> text(s, "title"))
            .thenComparing(s -> text(s, "id")));
    return all;
  }

  private static void decorateSource(ObjectNode source) {
    String kind = text(source, "kind");
    source.put(
        "navigationGroup",
        kind.equals("FAVORITE")
            ? "FAVORITES"
            : kind.equals("COLLECTION") ? "COLLECTIONS" : "OTHER");
    String label =
        switch (kind) {
          case "FAVORITE" -> "收藏夹";
          case "COLLECTION" -> "合集";
          case "MOVIE" -> "电影";
          case "SERIES" -> "番剧";
          default -> "用户添加视频";
        };
    String name = text(source, "alias").isBlank() ? text(source, "title") : text(source, "alias");
    source.put("displayLabel", label + "-" + name);
  }

  public ObjectNode publicSource(JsonNode source) {
    ObjectNode copy = (ObjectNode) source.deepCopy();
    copy.remove("externalId");
    return copy;
  }

  public void importSource(JsonNode operation, JsonNode result) throws SQLException {
    String sourceId = required(operation.path("payload"), "sourceId");
    ObjectNode source = store.get("source", sourceId);
    JsonNode info = result.path("source");
    if (info.isObject() && !text(source, "kind").equals("USER_ADDED")) {
      if (info.hasNonNull("title")) source.put("title", required(info, "title"));
    }
    if (!result.path("contents").isArray())
      throw Failure.bad("INVALID_INPUT", "Metadata contents required");
    Set<String> imported = new HashSet<>();
    for (JsonNode content : result.path("contents")) imported.add(importContent(content, sourceId));
    if (text(source, "kind").equals("SERIES")
        && result.path("contents").size() == 1
        && text(result.path("contents").get(0), "kind").equals("MOVIE"))
      source.put("kind", "MOVIE");
    if (result.path("complete").asBoolean(false)) {
      // Only a complete source scan may remove a source relationship; content and artifacts
      // survive.
      for (JsonNode value : store.all("content")) {
        if (imported.contains(text(value, "id"))) continue;
        List<String> relations = strings(value.path("sourceIds"));
        if (relations.remove(sourceId)) {
          ObjectNode content = (ObjectNode) value;
          content.set("sourceIds", MAPPER.valueToTree(relations));
          store.put("content", text(content, "id"), content);
        }
      }
      source.put("status", "READY");
      source.put("lastSyncedAt", store.now());
    } else source.put("status", "PARTIAL");
    source.put(
        "itemCount",
        store.all("content").stream()
            .filter(c -> strings(c.path("sourceIds")).contains(sourceId))
            .count());
    source.put("revision", Long.toString(Long.parseLong(text(source, "revision")) + 1));
    decorateSource(source);
    store.put("source", sourceId, source);
    bump();
  }

  public String importContent(JsonNode input, String sourceId) throws SQLException {
    String external = required(input, "externalId"),
        type = required(input, "externalIdType"),
        kind = required(input, "kind"),
        incomingId = required(input, "id");
    if (!Set.of("BVID", "SEASON_ID", "EPISODE_ID", "MEDIA_ID").contains(type)
        || !Set.of("SINGLE", "MULTIPART", "SERIES", "MOVIE").contains(kind))
      throw Failure.bad("INVALID_INPUT", "Unsupported content identity");
    if (kind.equals("SERIES")
        && (!type.equals("SEASON_ID") || !external.equals(required(input, "seasonId"))))
      throw Failure.bad("INVALID_INPUT", "A series content must represent exactly one season");
    ObjectNode old = store.optional("content", incomingId);
    if (old != null
        && (!text(old, "externalId").equals(external) || !text(old, "externalIdType").equals(type)))
      throw Failure.conflict("CONTENT_IDENTITY_CONFLICT");
    for (JsonNode stored : store.all("content"))
      if (text(stored, "externalId").equals(external)
          && text(stored, "externalIdType").equals(type)) {
        old = (ObjectNode) stored;
        incomingId = text(old, "id");
        break;
      }
    Set<String> sourceIds = new TreeSet<>();
    if (old != null) sourceIds.addAll(strings(old.path("sourceIds")));
    if (sourceId != null) sourceIds.add(sourceId);
    if (input.path("publishedAt").isTextual()) Instant.parse(input.path("publishedAt").asText());
    String originalUrl = required(input, "originalUrl");
    URI original = URI.create(originalUrl);
    if (original.getHost() == null
        || !(original.getHost().equals("bilibili.com")
            || original.getHost().endsWith(".bilibili.com")))
      throw Failure.bad("INVALID_INPUT", "Invalid original URL");
    List<String> warnings = new ArrayList<>();
    for (JsonNode warning : input.path("warnings"))
      if (warning.isTextual() && warning.asText().length() <= 500) warnings.add(warning.asText());
    List<JsonNode> units = new ArrayList<>();
    Set<String> trackIds = new HashSet<>(), unitIds = new HashSet<>();
    if (!input.path("mediaUnits").isArray())
      throw Failure.bad("INVALID_INPUT", "mediaUnits required");
    for (JsonNode value : input.path("mediaUnits")) {
      String unitId = required(value, "id");
      if (!unitIds.add(unitId)) throw Failure.bad("INVALID_INPUT", "Duplicate media unit");
      List<JsonNode> tracks = new ArrayList<>();
      for (JsonNode track : value.path("tracks")) {
        String trackId = required(track, "id"), trackKind = required(track, "kind");
        if (!trackIds.add(trackId)) throw Failure.bad("INVALID_INPUT", "Duplicate track identity");
        if (!Set.of("VIDEO", "AUDIO", "SUBTITLE", "DANMAKU").contains(trackKind))
          throw Failure.bad("INVALID_INPUT", "Unknown track kind");
        String origin =
            trackKind.equals("SUBTITLE") ? text(track, "subtitleOrigin") : "NOT_APPLICABLE";
        if (trackKind.equals("SUBTITLE") && !origin.equals("HUMAN")) {
          if (!origin.equals("AI")) warnings.add("字幕类型尚未判明，暂不可选择");
          continue;
        }
        JsonNode size = track.path("sizeBytes");
        if (!size.isNull()
            && !size.isMissingNode()
            && (!size.isTextual() || !size.asText().matches("[0-9]+")))
          throw Failure.bad("INVALID_INPUT", "Invalid byte count");
        tracks.add(
            object(
                "id",
                trackId,
                "kind",
                trackKind,
                "available",
                track.path("available").asBoolean(false),
                "reasonCode",
                nullable(track, "reasonCode"),
                "language",
                nullable(track, "language"),
                "languageLabel",
                nullable(track, "languageLabel"),
                "subtitleOrigin",
                origin,
                "durationSeconds",
                number(track, "durationSeconds"),
                "sizeBytes",
                size.isTextual() ? size.asText() : null));
      }
      String unitKind = required(value, "kind");
      if (!Set.of("MAIN", "PART", "EPISODE", "LANGUAGE").contains(unitKind))
        throw Failure.bad("INVALID_INPUT", "Unknown media unit kind");
      units.add(
          object(
              "id",
              unitId,
              "contentId",
              incomingId,
              "externalId",
              required(value, "externalId"),
              "title",
              text(value, "title"),
              "index",
              Math.max(1, value.path("index").asInt()),
              "kind",
              unitKind,
              "language",
              nullable(value, "language"),
              "languageLabel",
              nullable(value, "languageLabel"),
              "durationSeconds",
              number(value, "durationSeconds"),
              "availability",
              availability(value),
              "availabilityReason",
              nullable(value, "availabilityReason"),
              "tracks",
              tracks));
    }
    ObjectNode content =
        object(
            "id",
            incomingId,
            "platform",
            "BILIBILI",
            "externalId",
            external,
            "externalIdType",
            type,
            "kind",
            kind,
            "revision",
            required(input, "revision"),
            "title",
            required(input, "title"),
            "coverUrl",
            nullable(input, "coverUrl"),
            "sourceIds",
            sourceIds,
            "originalUrl",
            originalUrl,
            "availability",
            availability(input),
            "availabilityReason",
            nullable(input, "availabilityReason"),
            "downloadSummary",
            old == null ? object("state", "NOT_REQUESTED") : old.path("downloadSummary"),
            "archiveSummary",
            old == null ? object("state", "NOT_REQUESTED") : old.path("archiveSummary"),
            "updatedAt",
            store.now(),
            "publishedAt",
            nullable(input, "publishedAt"),
            "author",
            input.path("author").isObject()
                ? object(
                    "id",
                    text(input.path("author"), "id"),
                    "name",
                    text(input.path("author"), "name"))
                : null,
            "description",
            text(input, "description"),
            "seasonId",
            nullable(input, "seasonId"),
            "mediaUnits",
            units,
            "warnings",
            warnings);
    store.put("content", incomingId, content);
    bump();
    return incomingId;
  }

  private String availability(JsonNode value) {
    String a = text(value, "availability");
    if (!Set.of("AVAILABLE", "REMOVED", "UNAVAILABLE", "UNKNOWN").contains(a)) return "UNKNOWN";
    if (a.equals("REMOVED") && !text(value, "availabilityReason").equals("RESOURCE_DELETED"))
      return "UNKNOWN";
    return a;
  }

  private static Object nullable(JsonNode value, String key) {
    return value.path(key).isTextual() ? value.path(key).asText() : null;
  }

  private static Object number(JsonNode value, String key) {
    if (value.path(key).isNumber() && value.path(key).asDouble() >= 0)
      return value.path(key).asDouble();
    return null;
  }

  public ObjectNode content(String id) throws SQLException {
    ObjectNode content = store.get("content", id);
    List<JsonNode> relations = new ArrayList<>();
    for (String sourceId : strings(content.path("sourceIds"))) {
      ObjectNode source = store.optional("source", sourceId);
      if (source != null) relations.add(publicSource(source));
    }
    content.set("sources", array(relations));
    List<String> states = new ArrayList<>();
    for (String[] row :
        store.rows(
            "SELECT status FROM operations WHERE type='DOWNLOAD_MEDIA' AND"
                + " json_extract(body,'$.payload.contentId')=? AND"
                + " json_extract(body,'$.payload.contentRevision')=?",
            id,
            text(content, "revision"))) states.add(row[0]);
    if (states.stream().anyMatch(s -> Set.of("RUNNING", "QUEUED", "WAITING_RETRY").contains(s)))
      content.set(
          "downloadSummary",
          object("state", states.contains("RUNNING") ? "DOWNLOADING" : "QUEUED"));
    else if (states.stream().anyMatch(s -> Set.of("NEEDS_ACTION", "FAILED").contains(s)))
      content.set("downloadSummary", object("state", "NEEDS_ACTION"));
    return content;
  }

  private void bump() throws SQLException {
    ObjectNode revision = store.optional("meta", "catalog");
    store.put(
        "meta",
        "catalog",
        object("revision", revision == null ? 1 : revision.path("revision").asLong() + 1));
  }

  private String revision() throws SQLException {
    ObjectNode r = store.optional("meta", "catalog");
    return r == null ? "0" : r.path("revision").asText();
  }

  public List<JsonNode> filtered(Map<String, String> query) throws SQLException {
    String sort = query.getOrDefault("sort", "PUBLISHED_DESC");
    if (!Set.of("TITLE_ASC", "TITLE_DESC", "PUBLISHED_ASC", "PUBLISHED_DESC").contains(sort))
      throw Failure.bad("INVALID_INPUT", "Unsupported sort");
    Instant from = date(query.get("from"), false), to = date(query.get("to"), true);
    if (from != null && to != null && !from.isBefore(to))
      throw Failure.bad("INVALID_INPUT", "Invalid date interval");
    List<JsonNode> out = new ArrayList<>();
    for (JsonNode value : store.all("content")) {
      if (query.containsKey("sourceId")
          && !strings(value.path("sourceIds")).contains(query.get("sourceId"))) continue;
      if (query.containsKey("q")
          && !text(value, "title")
              .toLowerCase(Locale.ROOT)
              .contains(query.get("q").toLowerCase(Locale.ROOT))) continue;
      if (query.containsKey("type") && !text(value, "kind").equals(query.get("type"))) continue;
      Instant published =
          value.path("publishedAt").isTextual() ? Instant.parse(text(value, "publishedAt")) : null;
      if ((from != null || to != null)
          && (published == null
              || from != null && published.isBefore(from)
              || to != null && !published.isBefore(to))) continue;
      ObjectNode displayed = content(text(value, "id"));
      if (query.containsKey("state")
          && !text(displayed.path("downloadSummary"), "state").equals(query.get("state"))) continue;
      out.add(displayed);
    }
    Comparator<JsonNode> comparator;
    if (sort.startsWith("TITLE")) {
      comparator = Comparator.comparing(v -> text(v, "title"));
      if (sort.endsWith("DESC")) comparator = comparator.reversed();
    } else {
      Comparator<Instant> order =
          sort.endsWith("DESC") ? Comparator.reverseOrder() : Comparator.naturalOrder();
      comparator =
          Comparator.comparing(
              v -> v.path("publishedAt").isTextual() ? Instant.parse(text(v, "publishedAt")) : null,
              Comparator.nullsLast(order));
    }
    out.sort(comparator.thenComparing(v -> text(v, "id")));
    return out;
  }

  private Instant date(String value, boolean end) {
    if (value == null || value.isBlank()) return null;
    try {
      LocalDate date = LocalDate.parse(value);
      return (end ? date.plusDays(1) : date).atStartOfDay(ZONE).toInstant();
    } catch (Exception e) {
      throw Failure.bad("INVALID_INPUT", "日期必须为 yyyy-MM-dd");
    }
  }

  public ObjectNode contents(Map<String, String> query) throws SQLException {
    return page(filtered(query), query, revision());
  }

  public static ObjectNode page(
      List<? extends JsonNode> values, Map<String, String> query, String revision) {
    int size;
    try {
      size = Integer.parseInt(query.getOrDefault("pageSize", "50"));
    } catch (Exception e) {
      throw Failure.bad("INVALID_INPUT", "Invalid pageSize");
    }
    if (size < 1 || size > 200) throw Failure.bad("INVALID_INPUT", "pageSize must be 1–200");
    Map<String, String> filters = new TreeMap<>(query);
    filters.remove("cursor");
    String signature = PathPlanner.digest(canonical(MAPPER.valueToTree(filters)) + ":" + revision);
    int offset = 0;
    if (query.containsKey("cursor")) {
      try {
        JsonNode cursor =
            parse(
                new String(
                    Base64.getUrlDecoder().decode(query.get("cursor")), StandardCharsets.UTF_8));
        if (!text(cursor, "signature").equals(signature)) throw Failure.conflict("VERSION_CHANGED");
        offset = cursor.path("offset").asInt(-1);
        if (offset < 0 || offset > values.size())
          throw Failure.bad("INVALID_INPUT", "Invalid cursor");
      } catch (Failure e) {
        throw e;
      } catch (Exception e) {
        throw Failure.bad("INVALID_INPUT", "Invalid cursor");
      }
    }
    int end = Math.min(values.size(), offset + size);
    String next =
        end < values.size()
            ? Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(
                    object("offset", end, "signature", signature)
                        .toString()
                        .getBytes(StandardCharsets.UTF_8))
            : null;
    return object("items", values.subList(offset, end), "nextCursor", next, "total", values.size());
  }
}
