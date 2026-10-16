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

class ManagementHttpTest {
    @TempDir Path temp;
    String base;
    HttpClient client=HttpClient.newHttpClient();
    final List<JsonNode> captures=new ArrayList<>();
    HttpResponse<String> call(String method,String path,JsonNode body,Map<String,String> headers) throws Exception {
        var request=HttpRequest.newBuilder(URI.create(base+"/api/v1"+path)).header("Content-Type","application/json").timeout(java.time.Duration.ofSeconds(10));headers.forEach(request::header);
        return client.send(request.method(method,body==null?HttpRequest.BodyPublishers.noBody():HttpRequest.BodyPublishers.ofString(body.toString())).build(),HttpResponse.BodyHandlers.ofString());
    }
    JsonNode data(HttpResponse<String> response,int status) {assertEquals(status,response.statusCode(),response.body());return parse(response.body()).path("data");}
    void capture(String schema,JsonNode data) {captures.add(object("schema",schema,"data",data));}
    @Test void publicProjectCancellationHasStableVersionAndPersistsIdempotentScope() throws Exception {
        SpringApplication spring=new SpringApplication(BackendApplication.class);spring.setDefaultProperties(Map.of("server.port","0","alldownloads.database",temp.resolve("business.db").toString(),"alldownloads.runner-token-file",temp.resolve("test.token").toString(),"alldownloads.download-root","","spring.main.banner-mode","off","logging.level.root","ERROR"));
        try(var context=(ServletWebServerApplicationContext)spring.run()) {
            base="http://127.0.0.1:"+context.getWebServer().getPort();BusinessApplication app=context.getBean(BusinessApplication.class);
            JsonNode examples=MAPPER.readTree(Path.of("../../contracts/feedback1.examples.json").toFile());
            app.store.tx(()->{app.store.put("source","source-fixture",examples.path("source"));app.catalog.importContent(examples.path("content"),"source-fixture");app.store.put("target","default",object("id","default","revision","1","status","VALID","path",temp.toString(),"label",temp.toString(),"rootId","test-root","archivePolicy","AUTO","error",null));return null;});
            JsonNode preview=data(call("POST","/download-previews",object("platform","BILIBILI","selection",object("mode","EXPLICIT","resource","BILIBILI_CONTENTS","ids",List.of("content-fixture"))),Map.of("Idempotency-Key","preview")),202);
            JsonNode order=data(call("POST","/download-orders",object("previewId",preview.path("previewId")),Map.of("Idempotency-Key","order")),202);String id=order.path("projectIds").get(0).asText();
            var response=call("GET","/download-projects/"+id,null,Map.of());JsonNode project=data(response,200);capture("DownloadProject",project);String etag=response.headers().firstValue("ETag").orElseThrow();
            assertEquals(0,data(call("GET","/download-projects?platform=BILIBILI&contentId=missing",null,Map.of()),200).path("total").asInt());
            assertEquals(1,data(call("GET","/download-orders?platform=BILIBILI&contentId=content-fixture",null,Map.of()),200).path("total").asInt());assertEquals(0,data(call("GET","/download-orders?contentId=missing",null,Map.of()),200).path("total").asInt());
            assertEquals(409,call("POST","/download-projects/"+id+"/actions",object("action","CANCEL"),Map.of("If-Match","\"stale\"","Idempotency-Key","stale")).statusCode());
            JsonNode stopped=data(call("POST","/download-projects/"+id+"/actions",object("action","CANCEL"),Map.of("If-Match",etag,"Idempotency-Key","cancel")),202);assertEquals("CANCELLED",text(stopped,"status"));capture("DownloadProject",stopped);
            assertEquals(stopped,data(call("POST","/download-projects/"+id+"/actions",object("action","CANCEL"),Map.of("If-Match",etag,"Idempotency-Key","cancel")),202));
            assertEquals(409,call("POST","/download-projects/"+id+"/actions",object("action","CANCEL"),Map.of("If-Match","\"different\"","Idempotency-Key","cancel")).statusCode());
            assertEquals(0,data(call("GET","/archives",null,Map.of()),200).path("total").asInt());
            JsonNode selection=data(call("POST","/directory-selections",object(),Map.of("Idempotency-Key","select")),202);JsonNode unavailable=data(call("GET","/directory-selections/"+text(selection,"selectionId"),null,Map.of()),200);capture("DirectorySelection",unavailable);assertEquals("UNAVAILABLE",text(unavailable,"status"));
            assertEquals(400,call("POST","/directory-selections",object("path","C:\\client"),Map.of("Idempotency-Key","client-path")).statusCode());
            capture("Order",data(call("GET","/download-orders/"+text(order,"orderId"),null,Map.of()),200));
            Files.createDirectories(Path.of("target"));MAPPER.writerWithDefaultPrettyPrinter().writeValue(Path.of("target/management-captures.json").toFile(),captures);
        }
    }
}
