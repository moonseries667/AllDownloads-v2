package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.json.JsonMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.util.*;

/** Small JSON boundary shared by persistent snapshots and HTTP; no private platform locators. */
public final class Json {
    public static final ObjectMapper MAPPER = JsonMapper.builder().findAndAddModules().build();
    private Json() {}
    public static ObjectNode object(Object... pairs) {
        ObjectNode value=MAPPER.createObjectNode();
        for(int i=0;i<pairs.length;i+=2) value.set((String)pairs[i],MAPPER.valueToTree(pairs[i+1]));
        return value;
    }
    public static ArrayNode array(Collection<? extends JsonNode> values) { return MAPPER.valueToTree(values); }
    public static JsonNode parse(String text) {
        try { return MAPPER.readTree(text); } catch(Exception e) { throw new IllegalStateException("Invalid persisted JSON",e); }
    }
    public static String text(JsonNode n,String key) { return n.path(key).asText(""); }
    public static String required(JsonNode n,String key) {
        if(!n.path(key).isTextual() || text(n,key).isBlank()) throw Failure.bad("INVALID_INPUT",key+" is required");
        return text(n,key);
    }
    public static List<JsonNode> list(JsonNode n) { List<JsonNode> result=new ArrayList<>(); if(n.isArray()) n.forEach(result::add); return result; }
    public static List<String> strings(JsonNode n) {
        if(!n.isArray()) throw Failure.bad("INVALID_INPUT","Expected an array");
        List<String> values=new ArrayList<>();
        for(JsonNode v:n) { if(!v.isTextual()||v.asText().isBlank()) throw Failure.bad("INVALID_INPUT","Expected nonempty identifiers"); values.add(v.asText()); }
        return values;
    }
    public static String canonical(JsonNode n) {
        if(n.isObject()) { TreeMap<String,JsonNode> sorted=new TreeMap<>(); n.fields().forEachRemaining(e->sorted.put(e.getKey(),parse(canonical(e.getValue())))); return MAPPER.valueToTree(sorted).toString(); }
        if(n.isArray()) { ArrayNode out=MAPPER.createArrayNode(); n.forEach(v->out.add(parse(canonical(v))));return out.toString(); }
        return n.toString();
    }
    public static String id() { return UUID.randomUUID().toString(); }
}
