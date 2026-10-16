package dev.alldownloads.http;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.BackendApplication;
import dev.alldownloads.business.BusinessApplication;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.web.servlet.context.ServletWebServerApplicationContext;
import java.net.URI;
import java.net.http.*;
import java.nio.file.*;
import java.util.*;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class FeedbackHttpTest {
    @TempDir Path temp;
    @Test void publicFoldersWatcherAndIssuesPreserveTruthAndProduceContractModels() throws Exception {
        SpringApplication spring=new SpringApplication(BackendApplication.class);spring.setDefaultProperties(Map.of("server.port","0","alldownloads.database",temp.resolve("business.db").toString(),"alldownloads.runner-token-file",temp.resolve("runner.token").toString(),"spring.main.banner-mode","off","logging.level.root","ERROR"));
        try(var context=(ServletWebServerApplicationContext)spring.run()) {
            String base="http://127.0.0.1:"+context.getWebServer().getPort();HttpClient client=HttpClient.newHttpClient();var app=context.getBean(BusinessApplication.class);List<JsonNode> captures=new ArrayList<>();
            var watcher=call(client,base,"GET","/api/v1/telegram/watcher?accountId=tel-test",null,Map.of());assertEquals(200,watcher.statusCode());assertEquals("\"0\"",watcher.headers().firstValue("ETag").orElseThrow());capture(captures,"SavedWatcher",watcher);
            assertEquals(409,call(client,base,"PATCH","/api/v1/telegram/watcher",object("accountId","tel-test","enabled",true),Map.of("If-Match","\"0\"")).statusCode());
            assertEquals(410,call(client,base,"POST","/api/v1/telegram/link-downloads",object("links",List.of("https://t.me/example/1")),Map.of()).statusCode());
            assertEquals(0,app.store.tx(()->app.store.all("task").size()));
            var loading=call(client,base,"GET","/api/v1/quark/folders?accountId=quark-test",null,Map.of());assertEquals("LOADING",parse(loading.body()).path("data").path("status").asText());capture(captures,"FolderListing",loading);
            ObjectNode root=(ObjectNode)MAPPER.readTree(Path.of("../../contracts/feedback2.examples.json").toFile()).path("quarkRoot");app.store.tx(()->{app.feedback.quark.acceptTree(root);return null;});
            var folders=call(client,base,"GET","/api/v1/quark/folders?accountId=quark-test",null,Map.of());assertEquals("我的云盘",parse(folders.body()).path("data").path("parent").path("name").asText());capture(captures,"FolderListing",folders);
            ObjectNode issue=app.store.tx(()->{String task=app.queue.enqueue("LIST_QUARK_TREE","QUARK","failure",null,"1",object("schemaVersion",1));app.queue.issue(task,"folder","PERMISSION_DENIED","ERROR");return (ObjectNode)app.feedback.issues.list(false).get(0);});
            String task=text(issue,"taskId"),before=app.store.tx(()->app.store.get("task",task)).toString();var acknowledged=call(client,base,"POST","/api/v1/issues/"+text(issue,"id")+"/acknowledge",null,Map.of());capture(captures,"Issue",acknowledged);assertTrue(parse(acknowledged.body()).path("data").path("read").asBoolean());assertFalse(parse(acknowledged.body()).path("data").path("resolved").asBoolean());assertEquals(before,app.store.tx(()->app.store.get("task",task)).toString());
            app.store.tx(()->{app.queue.issue(task,"folder","PERMISSION_DENIED","ERROR");return null;});var reloaded=call(client,base,"GET","/api/v1/issues",null,Map.of());assertEquals(1,parse(reloaded.body()).path("data").path("items").size());assertTrue(parse(reloaded.body()).path("data").path("items").get(0).path("read").asBoolean());
            assertEquals(0,app.store.tx(()->app.store.all("order").size()));Files.writeString(Path.of("target/feedback2-contract-captures.json"),array(captures).toPrettyString());
        }
    }
    private static void capture(List<JsonNode> out,String schema,HttpResponse<String> response){assertEquals(200,response.statusCode());out.add(object("schema",schema,"data",parse(response.body()).path("data")));}
    private static HttpResponse<String> call(HttpClient client,String base,String method,String path,JsonNode body,Map<String,String> headers)throws Exception{var req=HttpRequest.newBuilder(URI.create(base+path)).header("Content-Type","application/json").timeout(java.time.Duration.ofSeconds(10));headers.forEach(req::header);return client.send(req.method(method,body==null?HttpRequest.BodyPublishers.noBody():HttpRequest.BodyPublishers.ofString(body.toString())).build(),HttpResponse.BodyHandlers.ofString());}
}
