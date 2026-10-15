package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.sql.SQLException;
import java.util.*;
import static dev.alldownloads.business.Json.*;

/** All naming policy lives in Java. Assignments survive metadata refreshes. */
public final class PathPlanner {
    private final BusinessStore store;
    public PathPlanner(BusinessStore store) { this.store=store; }
    public static String safe(String input) {
        String value=input.replaceAll("[<>:\"/\\\\|?*\\p{Cntrl}]","_").replace('（','(').replace('）',')').strip().replaceAll("[ .]+$","");
        if(value.isBlank()||value.equals(".")||value.equals("..")) value="未命名";
        String base=value.split("\\.",2)[0].toUpperCase(Locale.ROOT);
        if(base.matches("CON|PRN|AUX|NUL|CONIN\\$|CONOUT\\$|COM[1-9¹²³]|LPT[1-9¹²³]")||value.equalsIgnoreCase(".work")) value="_"+value;
        if(value.length()>65) value=value.substring(0,52)+"-"+digest(input).substring(0,10);
        return value;
    }
    public static String digest(String text) { try { return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(text.getBytes(StandardCharsets.UTF_8))); } catch(Exception e) { throw new IllegalStateException(e); } }
    public String assign(String identity,String candidate) throws SQLException {
        String[] old=store.row("SELECT path FROM paths WHERE identity=?",identity);if(old!=null)return old[0];
        String result=candidate;
        if(store.row("SELECT identity FROM paths WHERE path=? COLLATE NOCASE",result)!=null) result=withSuffix(candidate,digest(identity).substring(0,10));
        if(result.length()>235) throw new Failure(422,"INVALID_STORAGE_ROOT","目标相对路径过长，请缩短名称");
        store.update("INSERT INTO paths VALUES(?,?)",identity,result);return result;
    }
    private static String withSuffix(String path,String suffix) {
        int slash=path.lastIndexOf('/'),dot=path.lastIndexOf('.');
        if(dot>slash) return path.substring(0,dot)+"-"+suffix+path.substring(dot);
        return path+"-"+suffix;
    }
    public String content(JsonNode content) throws SQLException {
        List<JsonNode> sources=list(content.path("sources"));sources.sort(Comparator.comparing(v->text(v,"id")));
        JsonNode source=sources.isEmpty()?object("id","user-added","kind","USER_ADDED","title","用户添加视频"):sources.getFirst();
        String type=switch(text(source,"kind")) { case "FAVORITE"->"收藏夹";case "COLLECTION"->"合集";case "SERIES"->"番剧";case "MOVIE"->"电影";default->"用户添加"; };
        String sourcePath=assign("source:"+text(source,"id"),"Bilibili/"+safe(text(source,"title"))+"("+type+")");
        return assign("content:"+text(content,"id"),sourcePath+"/"+safe(text(content,"title"))+"("+safe(text(content,"externalId"))+")");
    }
    public String unit(JsonNode content,JsonNode unit) throws SQLException {
        String base=content(content),kind=text(content,"kind"),title=text(unit,"title");
        if(kind.equals("SINGLE")||kind.equals("MOVIE")&&content.path("mediaUnits").size()==1&&text(unit,"kind").equals("MAIN"))return base;
        int index=unit.path("index").asInt(1);String leaf;
        if(kind.equals("MULTIPART"))leaf=String.format(Locale.ROOT,"P%02d",index)+(title.isBlank()?"":"-"+safe(title));
        else if(kind.equals("SERIES"))leaf=title.isBlank()?String.format(Locale.ROOT,"第%02d集(%s)",index,safe(text(unit,"externalId"))):safe(title);
        else leaf=!title.isBlank()?safe(title):!text(unit,"languageLabel").isBlank()?safe(text(unit,"languageLabel")):"电影("+safe(text(unit,"externalId"))+")";
        return assign("unit:"+text(content,"id")+":"+text(unit,"id"),base+"/"+leaf);
    }
    public String output(String identity,String candidate) throws SQLException { return assign("output:"+identity,candidate); }
    public String quark(String shareId,String externalId,String relativePath,String nodeId) throws SQLException {
        String prefix=externalId.length()>8?externalId.substring(externalId.length()-8):externalId;
        String share=assign("share:"+shareId,"Quark/"+safe(prefix));
        List<String> parts=new ArrayList<>();for(String p:relativePath.replace('\\','/').split("/",-1)) { if(p.isBlank()||p.equals(".")||p.equals(".."))throw Failure.bad("INVALID_INPUT","Invalid cloud relative path");parts.add(safe(p)); }
        return assign("quark-node:"+nodeId,share+"/"+String.join("/",parts));
    }
}
