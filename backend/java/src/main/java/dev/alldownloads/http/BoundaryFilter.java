package dev.alldownloads.http;

import dev.alldownloads.business.Json;
import jakarta.servlet.*;
import jakarta.servlet.http.*;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;
import java.io.IOException;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.util.*;

/** Local browser and runner boundaries are separate; private token is never returned by public routes. */
@Component
public class BoundaryFilter extends OncePerRequestFilter {
    private final byte[] runnerToken;
    private final Set<String> origins;
    public BoundaryFilter(@Value("${alldownloads.runner-token-file:runtime/control/runner.token}") String tokenFile,@Value("${alldownloads.allowed-origins:}") String origins) throws IOException {
        Path path=Path.of(tokenFile).toAbsolutePath().normalize();Files.createDirectories(path.getParent());
        if(!Files.exists(path)) {byte[] random=new byte[32];new SecureRandom().nextBytes(random);try {Files.writeString(path,Base64.getUrlEncoder().withoutPadding().encodeToString(random),StandardOpenOption.CREATE_NEW);}catch(FileAlreadyExistsException race) { /* The existing private configuration wins. */ }}
        String token=Files.readString(path).strip();if(token.length()<32)throw new IOException("Runner token file must contain at least 32 characters");runnerToken=("Bearer "+token).getBytes(StandardCharsets.UTF_8);this.origins=new HashSet<>();for(String origin:origins.split(","))if(!origin.isBlank())this.origins.add(origin.strip());
    }
    @Override protected void doFilterInternal(HttpServletRequest request,HttpServletResponse response,FilterChain chain) throws ServletException,IOException {
        String path=request.getRequestURI(),origin=request.getHeader("Origin");
        if(path.startsWith("/internal/")) {
            String authorization=request.getHeader("Authorization");
            if(origin!=null||!InetAddress.getByName(request.getRemoteAddr()).isLoopbackAddress()||authorization==null||!MessageDigest.isEqual(runnerToken,authorization.getBytes(StandardCharsets.UTF_8))) {deny(response,401,"AUTH_REQUIRED","内部执行身份校验失败");return;}
        } else if(path.startsWith("/api/")) {
            String self=request.getScheme()+"://"+request.getHeader("Host");
            if(origin!=null&&!origin.equals(self)&&!origins.contains(origin)) {deny(response,403,"ORIGIN_NOT_ALLOWED","此页面来源不允许访问业务接口");return;}
            if(request.getHeader("Authorization")!=null&&MessageDigest.isEqual(runnerToken,request.getHeader("Authorization").getBytes(StandardCharsets.UTF_8))) {deny(response,403,"WRONG_IDENTITY","执行身份不能调用浏览器接口");return;}
            if(origin!=null&&origins.contains(origin)) {response.setHeader("Access-Control-Allow-Origin",origin);response.setHeader("Vary","Origin");response.setHeader("Access-Control-Allow-Headers","Content-Type,Idempotency-Key,If-Match");response.setHeader("Access-Control-Allow-Methods","GET,POST,PATCH,OPTIONS");response.setHeader("Access-Control-Expose-Headers","ETag");if(request.getMethod().equals("OPTIONS")){response.setStatus(204);return;}}
        }
        chain.doFilter(request,response);
    }
    private void deny(HttpServletResponse response,int status,String code,String message) throws IOException {response.setStatus(status);response.setContentType("application/json;charset=UTF-8");response.getWriter().write(Json.object("error",Json.object("code",code,"message",message,"retryable",false,"action",null,"details",Json.object()),"requestId",Json.id()).toString());}
}
