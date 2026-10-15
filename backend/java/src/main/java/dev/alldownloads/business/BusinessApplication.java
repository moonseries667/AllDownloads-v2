package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.sql.SQLException;
import java.time.Instant;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** Wires the single queue to business handlers without allowing metadata paths to create media tasks. */
public final class BusinessApplication {
    public final BusinessStore store;
    public final OperationQueue queue;
    public final CatalogService catalog;
    public final DownloadService downloads;
    public BusinessApplication(BusinessStore store,String defaultRoot) {
        this.store=store;queue=new OperationQueue(store);catalog=new CatalogService(store,queue);downloads=new DownloadService(store,catalog,queue);
        queue.onCompletion(this::complete);
        store.tx(()->{downloads.configureDefault(defaultRoot);queue.expire();return null;});
    }
    private void complete(JsonNode operation,JsonNode result) throws SQLException {
        switch(text(operation,"operationType")) {
            case "VALIDATE_TARGET" -> downloads.targetResult(operation,result);
            case "SYNC_BIL_SOURCE" -> catalog.importSource(operation,result);
            case "FETCH_BIL_METADATA" -> catalog.importContent(result.path("content"),null);
            case "DOWNLOAD_MEDIA" -> downloads.downloadResult(operation,result);
            // No cloud adapter is enabled in this increment: never accept a bare success as archive proof.
            default -> throw Failure.conflict("UNSUPPORTED_RESULT");
        }
    }
    public ObjectNode overview() throws SQLException {
        queue.expire();List<JsonNode> services=new ArrayList<>();services.add(object("id","java","required",true,"state","ACTIVE","reasonCode",null,"lastSeenAt",store.now()));
        List<JsonNode> runners=store.all("runner");JsonNode newest=runners.stream().max(Comparator.comparing(r->text(r,"lastSeenAt"))).orElse(null);
        boolean active=newest!=null&&Instant.parse(text(newest,"lastSeenAt")).isAfter(store.clock.instant().minusSeconds(90));
        boolean ready=active&&strings(newest.path("capabilities")).containsAll(List.of("VALIDATE_TARGET","SYNC_BIL_SOURCE","FETCH_BIL_METADATA","DOWNLOAD_MEDIA"));
        String goState=active?(ready?"ACTIVE":"ERROR"):newest==null?"NOT_CONFIGURED":"LOST";
        services.add(object("id","go","required",true,"state",goState,"reasonCode",ready?null:active?"RUNNER_CAPABILITY_MISSING":newest==null?"RUNNER_NOT_REGISTERED":"RUNNER_UNAVAILABLE","lastSeenAt",newest==null?null:newest.path("lastSeenAt")));
        ObjectNode mount=object("state","UNKNOWN","reasonCode","NO_MOUNT_OBSERVATION","observedAt",null);
        if(active&&newest.path("facts").path("mount").isObject()) {
            JsonNode fact=newest.path("facts").path("mount");String state=text(fact,"state");
            if(Set.of("MOUNTED","NOT_MOUNTED","NOT_CONFIGURED","UNKNOWN","UNSUPPORTED").contains(state))mount=object("state",state,"reasonCode",fact.path("reasonCode").isTextual()?fact.path("reasonCode").asText():null,"observedAt",fact.path("observedAt").isTextual()?fact.path("observedAt").asText():null);
        }
        long download=store.all("task").stream().filter(t->text(t,"kind").equals("DOWNLOAD")&&text(t,"status").equals("SUCCEEDED")).count();long archive=store.all("file").stream().filter(f->text(f,"archiveState").equals("VERIFIED")).count();
        boolean refreshing=store.all("task").stream().anyMatch(t->text(t,"phase").equals("SYNC_BIL_SOURCE")&&Set.of("QUEUED","RUNNING","WAITING_RETRY").contains(text(t,"status")));
        return object("runtime",object("state",ready?"RUNNING":"PARTIAL","observedAt",store.now(),"services",services,"mount",mount),"bilibili",object("accountState",active&&newest.path("facts").path("accountState").isTextual()?text(newest.path("facts"),"accountState"):"UNKNOWN","refreshState",refreshing?"REFRESHING":"IDLE","downloadCount",download,"archiveCount",archive));
    }
}
