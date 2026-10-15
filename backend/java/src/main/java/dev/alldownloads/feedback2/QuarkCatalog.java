package dev.alldownloads.feedback2;

import com.fasterxml.jackson.databind.*;
import com.fasterxml.jackson.databind.node.*;
import java.net.URI;
import java.sql.*;
import java.time.Clock;
import java.util.*;

/** Stable cloud identities and immutable transfer targets; never interprets a local download path. */
public final class QuarkCatalog extends DomainSql {
    public QuarkCatalog(Connection db,ObjectMapper json,Clock clock) { super(db,json,clock); }
    public void initialize() throws SQLException { ddl(
        "CREATE TABLE IF NOT EXISTS f2_quark_folders(account_id TEXT NOT NULL,id TEXT NOT NULL,parent_id TEXT,body TEXT NOT NULL,PRIMARY KEY(account_id,id))",
        "CREATE TABLE IF NOT EXISTS f2_quark_scopes(account_id TEXT NOT NULL,parent_id TEXT NOT NULL,complete INTEGER NOT NULL,PRIMARY KEY(account_id,parent_id))",
        "CREATE TABLE IF NOT EXISTS f2_quark_shares(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,external_id TEXT NOT NULL,folder_key TEXT NOT NULL COLLATE NOCASE UNIQUE,body TEXT NOT NULL,tree_complete INTEGER NOT NULL DEFAULT 0,UNIQUE(account_id,external_id))",
        "CREATE TABLE IF NOT EXISTS f2_quark_nodes(share_id TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(share_id,id))"); }
    public void acceptTree(ObjectNode result) throws SQLException {
        writing();String account=text(result,"accountId"),parent=nullable(result,"parentId"),share=nullable(result,"shareId"); boolean complete=result.path("complete").asBoolean();
        require(result.path("folders").isArray(),"PROVIDER_CHANGED"); Set<String> included=new HashSet<>();
        for(var f:result.path("folders")) {
            require(account.equals(text(f,"accountId")),"PROVIDER_CHANGED"); require("FOLDER".equals(text(f,"kind")),"PROVIDER_CHANGED");
            String id=text(f,"id"),p=nullable(f,"parentId"); require(!id.equals(p),"PROVIDER_CHANGED"); validateName(text(f,"name")); text(f,"revision");
            require(included.add(id),"PROVIDER_CHANGED"); ObjectNode value=((ObjectNode)f).deepCopy(); value.remove("path");
            exec("INSERT INTO f2_quark_folders VALUES(?,?,?,?) ON CONFLICT(account_id,id) DO UPDATE SET parent_id=excluded.parent_id,body=excluded.body",account,id,p,value.toString());
        }
        List<String[]> roots=rows("SELECT id FROM f2_quark_folders WHERE account_id=? AND parent_id IS NULL",account); require(roots.size()==1,"PROVIDER_CHANGED");
        String actualParent=parent==null?roots.getFirst()[0]:parent; folder(account,actualParent); // Checks complete, acyclic parent chain.
        for(String id:included) folder(account,id);
        if(complete && share==null) for(var r:rows("SELECT id,body FROM f2_quark_folders WHERE account_id=? AND parent_id=?",account,actualParent)) if(!included.contains(r[0])) {
            ObjectNode removed=read(r[1]).put("availability","REMOVED").put("selectable",false).put("availabilityReason","目标文件夹已删除或移动。");
            exec("UPDATE f2_quark_folders SET body=? WHERE account_id=? AND id=?",removed.toString(),account,r[0]);
        }
        if(share==null)exec("INSERT INTO f2_quark_scopes VALUES(?,?,?) ON CONFLICT(account_id,parent_id) DO UPDATE SET complete=excluded.complete",account,actualParent,complete?1:0);
        if(share!=null) acceptNodes(share,result.path("nodes"),complete);
    }
    public ObjectNode folders(String account,String parent) throws SQLException {
        var roots=rows("SELECT id FROM f2_quark_folders WHERE account_id=? AND parent_id IS NULL",account);
        if(roots.isEmpty()) return emptyListing("LOADING");
        require(roots.size()==1,"PROVIDER_CHANGED"); String id=parent==null?roots.getFirst()[0]:parent; ObjectNode current=folder(account,id);
        ArrayNode items=array(),breadcrumbs=array(); List<ObjectNode> chain=chain(account,id); for(var node:chain) breadcrumbs.add(folder(account,node.path("id").asText()));
        for(var r:rows("SELECT id FROM f2_quark_folders WHERE account_id=? AND parent_id=? ORDER BY id",account,id)) {
            ObjectNode child=folder(account,r[0]); if(!child.path("availability").asText().equals("REMOVED")) items.add(child);
        }
        List<JsonNode> sorted=new ArrayList<>(); items.forEach(sorted::add); sorted.sort(Comparator.comparing((JsonNode n)->n.path("name").asText()).thenComparing(n->n.path("id").asText()));items=array();sorted.forEach(items::add);
        var scope=row("SELECT complete FROM f2_quark_scopes WHERE account_id=? AND parent_id=?",account,id);
        ObjectNode out=emptyListing(!usable(current)?"NEEDS_ACTION":scope!=null&&scope[0].equals("1")?"READY":"LOADING"); out.set("items",items);out.set("parent",current);out.set("breadcrumbs",breadcrumbs);out.put("total",items.size());return out;
    }
    private ObjectNode emptyListing(String status) { ObjectNode o=obj().put("status",status).putNull("taskId").putNull("nextCursor").putNull("total");o.set("items",array());o.set("breadcrumbs",array());o.putNull("parent");return o; }
    public ObjectNode folder(String account,String id) throws SQLException {
        List<ObjectNode> chain=chain(account,id); ObjectNode out=chain.getLast().deepCopy(); StringJoiner path=new StringJoiner("/");
        for(int i=1;i<chain.size();i++)path.add(chain.get(i).path("name").asText()); out.put("path",path.toString());
        if(chain.size()==1) out.put("name","我的云盘");
        if(chain.stream().anyMatch(n->!usable(n))) out.put("selectable",false).put("availability",out.path("availability").asText().equals("AVAILABLE")?"UNAVAILABLE":out.path("availability").asText()).put("availabilityReason","目标文件夹或父级目录不可用。");
        return out;
    }
    private List<ObjectNode> chain(String account,String id) throws SQLException {
        Set<String> seen=new HashSet<>();LinkedList<ObjectNode> out=new LinkedList<>();String current=id;
        while(current!=null) { require(seen.add(current),"PROVIDER_CHANGED"); var r=row("SELECT body,parent_id FROM f2_quark_folders WHERE account_id=? AND id=?",account,current);require(r!=null,"TARGET_UNAVAILABLE");out.addFirst(read(r[0]));current=r[1]; }
        return out;
    }
    public ObjectNode addShares(ObjectNode request,Enqueue enqueue) throws SQLException {
        writing();String account=text(request,"accountId");JsonNode entries=request.path("entries");require(entries.isArray()&&!entries.isEmpty()&&entries.size()<=200,"INVALID_INPUT");
        ArrayNode items=array();
        for(var entry:entries) {
            ObjectNode item=obj();
            try {
                require(!entry.has("targetPath")&&!entry.has("path"),"INVALID_INPUT");String external=shareId(text(entry,"url"));
                ObjectNode target=folder(account,text(entry,"targetFolderId"));require(usable(target),"TARGET_UNAVAILABLE");require(target.path("revision").asText().equals(text(entry,"targetFolderRevision")),"VERSION_CHANGED");
                var old=row("SELECT body FROM f2_quark_shares WHERE account_id=? AND external_id=?",account,external);
                if(old!=null) { ObjectNode saved=read(old[0]); require(saved.path("targetFolderId").asText().equals(target.path("id").asText())&&saved.path("targetFolderRevision").asText().equals(target.path("revision").asText()),"SHARE_TARGET_CONFLICT");item.put("shareId",saved.path("id").asText()).put("taskId",saved.path("taskId").asText()).put("reused",true).putNull("error"); }
                else {
                    String id=stable("share-",account,external),key=folderKey(external);ObjectNode share=obj().put("id",id).put("accountId",account).put("externalShareId",external).put("title",external).put("shareFolderKey",key).put("targetFolderId",target.path("id").asText()).put("targetFolderRevision",target.path("revision").asText()).put("targetFolderPath",target.path("path").asText()).put("status","QUEUED").put("watchEnabled",true).put("revision","1");share.set("target",target);
                    ObjectNode payload=obj().put("schemaVersion",1).put("shareId",id).put("url",text(entry,"url")).put("targetFolderId",target.path("id").asText()).put("targetFolderRevision",target.path("revision").asText());
                    String task=enqueue.add("TRANSFER_QUARK_ITEMS","QUARK",account,id,payload);share.put("taskId",task);
                    exec("INSERT INTO f2_quark_shares(id,account_id,external_id,folder_key,body) VALUES(?,?,?,?,?)",id,account,external,key,share.toString());item.put("shareId",id).put("taskId",task).put("reused",false).putNull("error");
                }
            } catch(DomainError e) { item.putNull("shareId").putNull("taskId"); item.set("error",obj().put("code",e.code).put("message","分享或目标目录不可用，请核对后重试。")); }
            items.add(item);
        }
        ObjectNode out=obj().put("batchId",UUID.randomUUID().toString());out.set("items",items);return out;
    }
    public ObjectNode share(String id) throws SQLException {var r=row("SELECT body FROM f2_quark_shares WHERE id=?",id);require(r!=null,"NOT_FOUND");return read(r[0]);}
    public ArrayNode shares() throws SQLException {ArrayNode a=array();for(var r:rows("SELECT body FROM f2_quark_shares ORDER BY id"))a.add(read(r[0]));return a;}
    public void transferResult(ObjectNode result) throws SQLException {
        writing();String id=text(result,"shareId");ObjectNode share=share(id);require(share.path("targetFolderId").asText().equals(text(result,"targetFolderId"))&&share.path("targetFolderRevision").asText().equals(text(result,"targetFolderRevision")),"VERIFICATION_FAILED");
        require(result.path("complete").asBoolean(),"RESULT_UNKNOWN");acceptNodes(id,result.path("nodes"),true);share.put("status","TRANSFERRED");exec("UPDATE f2_quark_shares SET body=? WHERE id=?",share.toString(),id);
    }
    public void acceptNodes(String shareId,JsonNode nodes,boolean complete) throws SQLException {
        writing();share(shareId);require(nodes.isArray(),"PROVIDER_CHANGED");Set<String> seen=new HashSet<>();
        for(var n:nodes) {require(shareId.equals(text(n,"shareId")),"PROVIDER_CHANGED");String id=text(n,"id");require(seen.add(id),"PROVIDER_CHANGED");text(n,"version");text(n,"externalId");validateName(text(n,"name"));require(Set.of("FILE","FOLDER").contains(text(n,"kind")),"PROVIDER_CHANGED");validateRelative(text(n,"relativePath"));if(n.hasNonNull("sizeBytes"))require(text(n,"sizeBytes").matches("0|[1-9][0-9]*"),"PROVIDER_CHANGED");
            exec("INSERT INTO f2_quark_nodes VALUES(?,?,?) ON CONFLICT(share_id,id) DO UPDATE SET body=excluded.body",shareId,id,n.toString()); }
        if(complete) for(var r:rows("SELECT id,body FROM f2_quark_nodes WHERE share_id=?",shareId))if(!seen.contains(r[0])) {ObjectNode removed=read(r[1]).put("availability","REMOVED").put("availabilityReason","网盘文件已不存在。");exec("UPDATE f2_quark_nodes SET body=? WHERE share_id=? AND id=?",removed.toString(),shareId,r[0]);}
        exec("UPDATE f2_quark_shares SET tree_complete=? WHERE id=?",complete?1:0,shareId);
    }
    public ArrayNode nodes(String shareId) throws SQLException { share(shareId);ArrayNode a=array();for(var r:rows("SELECT body FROM f2_quark_nodes WHERE share_id=? ORDER BY id",shareId))a.add(read(r[0]));return a; }
    /** Returns exact file versions; shared preview/order code freezes this result and the local target. */
    public ArrayNode expand(Set<String> ids,String queryShare,Set<String> exclude) throws SQLException {
        require(queryShare!=null || ids!=null&&!ids.isEmpty(),"EMPTY_SELECTION");ArrayNode out=array();Set<String> matched=new HashSet<>(),added=new HashSet<>();
        for(var sr:rows("SELECT id,tree_complete,folder_key FROM f2_quark_shares ORDER BY id")) {
            if(queryShare!=null&&!queryShare.equals(sr[0]))continue;ArrayNode all=nodes(sr[0]);Map<String,ObjectNode> byId=new LinkedHashMap<>();all.forEach(n->byId.put(n.path("id").asText(),(ObjectNode)n));
            Set<String> selected=new HashSet<>();for(String id:byId.keySet())if(queryShare!=null || ids.contains(id)) {selected.add(id);matched.add(id);}
            if(selected.isEmpty())continue;require("1".equals(sr[1]),"SNAPSHOT_INCOMPLETE");
            for(var n:byId.values()) {
                String id=n.path("id").asText();if(!n.path("kind").asText().equals("FILE")||exclude!=null&&exclude.contains(id))continue;
                String current=id;Set<String> visited=new HashSet<>();boolean include=false;
                while(current!=null) {
                    require(visited.add(current),"PROVIDER_CHANGED");if(exclude!=null&&exclude.contains(current)){include=false;break;}if(selected.contains(current))include=true;
                    ObjectNode p=byId.get(current);
                    if(p==null) { // Provider lists descendants, so the actual share root is a folder fact, not a content node.
                        folder(share(sr[0]).path("accountId").asText(),current);break;
                    }
                    current=nullable(p,"parentId");
                }
                if(include && added.add(id)) {ObjectNode frozen=n.deepCopy();frozen.put("plannedRelativePath","Quark/"+sr[2]+"/"+text(n,"relativePath"));out.add(frozen);}
            }
        }
        if(queryShare==null)require(matched.containsAll(ids),"NOT_FOUND");require(!out.isEmpty(),"EMPTY_SELECTION");return out;
    }
    public void validateFrozen(JsonNode nodes) throws SQLException {for(var n:nodes) {var r=row("SELECT body FROM f2_quark_nodes WHERE share_id=? AND id=?",text(n,"shareId"),text(n,"id"));require(r!=null,"VERSION_CHANGED");ObjectNode current=read(r[0]);require(current.path("version").equals(n.path("version")),"VERSION_CHANGED");require(current.path("availability").asText().equals("AVAILABLE"),"TARGET_UNAVAILABLE");}}
    private String folderKey(String external) throws SQLException {for(int len=Math.min(8,external.length());len<=external.length();len++){String key=external.substring(external.length()-len);if(row("SELECT id FROM f2_quark_shares WHERE folder_key=? COLLATE NOCASE",key)==null)return key;}return external+"-"+stable("",external).substring(0,8);}
    private static String shareId(String url) {try {URI u=URI.create(url);require("https".equalsIgnoreCase(u.getScheme())&&"pan.quark.cn".equalsIgnoreCase(u.getHost())&&u.getUserInfo()==null,"INVALID_INPUT");String path=u.getPath();require(path.matches("/s/[A-Za-z0-9]+/?"),"INVALID_INPUT");return path.substring(3).replace("/","");}catch(IllegalArgumentException e){throw new DomainError("INVALID_INPUT");}}
    private static boolean usable(JsonNode folder) {return folder.path("selectable").asBoolean()&&folder.path("availability").asText().equals("AVAILABLE");}
    private static void validateName(String name){require(!name.contains("/")&&!name.contains("\\")&&!name.equals(".")&&!name.equals(".."),"PROVIDER_CHANGED");}
    private static void validateRelative(String path){require(!path.startsWith("/")&&!path.contains("\\")&&!path.contains(":"),"PROVIDER_CHANGED");for(String s:path.split("/",-1))require(!s.isBlank()&&!s.equals(".")&&!s.equals(".."),"PROVIDER_CHANGED");}
}
