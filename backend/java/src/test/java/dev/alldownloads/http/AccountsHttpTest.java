package dev.alldownloads.http;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import dev.alldownloads.BackendApplication;
import dev.alldownloads.business.BusinessApplication;
import java.net.URI;
import java.net.http.*;
import java.nio.file.Path;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.web.servlet.context.ServletWebServerApplicationContext;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class AccountsHttpTest {
    @TempDir Path temp;
    @Test void realPublicHttpEnforcesIdempotencyReadPurityAndCancellationRevision() throws Exception {
        SpringApplication spring=new SpringApplication(BackendApplication.class);
        String frontendOrigin="http://127.0.0.1:5173";
        spring.setDefaultProperties(Map.of("server.port","0","alldownloads.database",temp.resolve("business.db").toString(),"alldownloads.runner-token-file",temp.resolve("runner.token").toString(),"alldownloads.download-root","","alldownloads.allowed-origins",frontendOrigin,"spring.main.banner-mode","off","logging.level.root","ERROR"));
        try(var context=(ServletWebServerApplicationContext)spring.run()) {
            String base="http://127.0.0.1:"+context.getWebServer().getPort();HttpClient client=HttpClient.newHttpClient();BusinessApplication app=context.getBean(BusinessApplication.class);
            var accounts=call(client,base,"GET","/accounts",null,Map.of());assertEquals(200,accounts.statusCode());assertEquals(4,data(accounts).path("items").size());assertTrue(app.store.tx(()->app.store.all("task")).isEmpty());
            assertEquals(400,call(client,base,"POST","/accounts/bilibili-main/check",null,Map.of()).statusCode());
            var checked=call(client,base,"POST","/accounts/bilibili-main/check",null,Map.of("Idempotency-Key","check-1"));assertEquals(202,checked.statusCode());assertEquals("bilibili-main",text(data(checked),"accountId"));
            assertEquals(data(checked),data(call(client,base,"POST","/accounts/bilibili-main/check",object(),Map.of("Idempotency-Key","check-1"))));
            assertEquals(404,call(client,base,"POST","/accounts/other/check",null,Map.of("Idempotency-Key","other")).statusCode());
            assertEquals(400,call(client,base,"POST","/accounts/bilibili-main/check",object("cookie","forbidden"),Map.of("Idempotency-Key","check-2")).statusCode());
            var begun=call(client,base,"POST","/auth-sessions",object("platform","BILIBILI"),Map.of("Idempotency-Key","start-1"));assertEquals(202,begun.statusCode());JsonNode session=data(begun);String path="/auth-sessions/"+text(session,"authSessionId"),etag=begun.headers().firstValue("ETag").orElseThrow();
            var preflight=call(client,base,"OPTIONS",path,null,Map.of("Origin",frontendOrigin,"Access-Control-Request-Method","DELETE","Access-Control-Request-Headers","If-Match"));assertEquals(204,preflight.statusCode());assertTrue(preflight.headers().firstValue("Access-Control-Allow-Methods").orElseThrow().contains("DELETE"));assertTrue(preflight.headers().firstValue("Access-Control-Allow-Headers").orElseThrow().contains("If-Match"));
            assertEquals("\"1\"",etag);assertEquals(session,data(call(client,base,"POST","/auth-sessions",object("platform","BILIBILI"),Map.of("Idempotency-Key","start-1"))));
            assertEquals(session,data(call(client,base,"POST","/auth-sessions",object("platform","BILIBILI"),Map.of("Idempotency-Key","start-2"))));
            assertEquals(409,call(client,base,"POST","/auth-sessions",object("platform","QUARK"),Map.of("Idempotency-Key","start-1")).statusCode());
            assertEquals(400,call(client,base,"POST","/auth-sessions",object("platform","BILIBILI","password","forbidden"),Map.of("Idempotency-Key","bad-start")).statusCode());
            int tasks=app.store.tx(()->app.store.all("task").size());var read=call(client,base,"GET",path,null,Map.of());assertEquals(session,data(read));assertEquals(tasks,app.store.tx(()->app.store.all("task").size()).intValue());
            assertEquals(400,call(client,base,"POST",path+"/steps",object("action","POLL","code","forbidden"),Map.of("Idempotency-Key","bad-step")).statusCode());
            var poll=call(client,base,"POST",path+"/steps",object("action","POLL"),Map.of("Idempotency-Key","poll-1"));assertEquals(202,poll.statusCode());assertEquals(session,data(poll));
            assertEquals(400,call(client,base,"DELETE",path,null,Map.of()).statusCode());assertEquals(409,call(client,base,"DELETE",path,null,Map.of("If-Match","\"0\"")).statusCode());
            var cancelled=call(client,base,"DELETE",path,null,Map.of("If-Match",etag,"Origin",frontendOrigin));assertEquals(202,cancelled.statusCode());assertEquals(frontendOrigin,cancelled.headers().firstValue("Access-Control-Allow-Origin").orElseThrow());assertEquals("ETag",cancelled.headers().firstValue("Access-Control-Expose-Headers").orElseThrow());assertEquals("CANCELLED",text(data(cancelled),"state"));assertNotEquals(etag,cancelled.headers().firstValue("ETag").orElseThrow());
            assertEquals(data(cancelled),data(call(client,base,"GET",path,null,Map.of())));assertEquals(409,call(client,base,"DELETE",path,null,Map.of("If-Match",etag)).statusCode());
            assertEquals(409,call(client,base,"POST",path+"/steps",object("action","POLL"),Map.of("Idempotency-Key","after-cancel")).statusCode());
            assertTrue(app.store.tx(()->app.store.all("order")).isEmpty());assertEquals("UNKNOWN",text(data(call(client,base,"GET","/accounts",null,Map.of())).path("items").get(0),"state"));
            java.nio.file.Files.writeString(Path.of("target/accounts-captures.json"),object("accounts",data(accounts).path("items"),"sessions",List.of(session,data(poll),data(cancelled))).toPrettyString());
        }
    }
    private static JsonNode data(HttpResponse<String> response) {return parse(response.body()).path("data");}
    private static HttpResponse<String> call(HttpClient client,String base,String method,String path,JsonNode body,Map<String,String> headers) throws Exception {
        HttpRequest.Builder request=HttpRequest.newBuilder(URI.create(base+"/api/v1"+path)).header("Content-Type","application/json").timeout(java.time.Duration.ofSeconds(10));headers.forEach(request::header);
        return client.send(request.method(method,body==null?HttpRequest.BodyPublishers.noBody():HttpRequest.BodyPublishers.ofString(body.toString())).build(),HttpResponse.BodyHandlers.ofString());
    }
}
