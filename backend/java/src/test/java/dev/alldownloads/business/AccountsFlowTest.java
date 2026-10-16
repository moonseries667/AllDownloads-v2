package dev.alldownloads.business;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.file.Path;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import static dev.alldownloads.business.Json.*;
import static org.junit.jupiter.api.Assertions.*;

class AccountsFlowTest {
    @TempDir Path temp;
    BusinessStore store;
    BusinessApplication app;
    MutableClock clock=new MutableClock();
    static final List<String> CAPS=List.of("CHECK_ACCOUNT","AUTH_STEP","SYNC_BIL_SOURCE","FETCH_BIL_METADATA","DOWNLOAD_MEDIA","LIST_DRIVE_FOLDERS");
    @BeforeEach void start() throws Exception {store=new BusinessStore(temp.resolve("control.db"),clock);app=new BusinessApplication(store,"");store.tx(()->app.queue.register(object("runnerId","fixture","protocolVersion",1,"capabilities",CAPS,"tools",object(),"facts",object("accountState","ACTIVE"))));}
    @AfterEach void close() throws Exception {if(store!=null)store.close();}
    JsonNode acquire(String capability) {return store.tx(()->app.queue.acquire(object("runnerId","fixture","capabilities",List.of(capability)))).path("operation");}
    ObjectNode begin(String platform) {return store.tx(()->app.auth.begin(object("platform",platform)));}
    ObjectNode session(JsonNode session) {return store.tx(()->app.auth.session(text(session,"authSessionId")));}
    ObjectNode facts(JsonNode session,String state,String action,String url,String reason) {return object("session",object("authSessionId",session.path("authSessionId"),"accountId",session.path("accountId"),"platform",session.path("platform"),"state",state,"nextAction",action,"url",url,"message","provider text must not be stored","expiresAt",session.path("expiresAt"),"reasonCode",reason));}
    ObjectNode request(JsonNode op,JsonNode facts) {return object("eventId",id(),"runnerId",op.path("runnerId"),"leaseId",op.path("leaseId"),"attempt",op.path("attempt"),"fencingToken",op.path("fencingToken"),"status","SUCCEEDED","result",facts,"error",null);}
    ObjectNode complete(JsonNode op,JsonNode facts) {ObjectNode request=request(op,facts);store.tx(()->app.queue.result(text(op,"operationId"),request,false));return request;}
    @Test void initialFactsAreUnknownPureAndChecksUseFixedAccountsWithoutOtherIo() {
        ObjectNode initial=store.tx(app.auth::accounts);assertEquals(4,initial.path("items").size());assertTrue(list(initial.path("items")).stream().allMatch(a->text(a,"state").equals("UNKNOWN")&&a.path("lastCheckedAt").isNull()));
        assertEquals("UNKNOWN",text(store.tx(app::overview).path("bilibili"),"accountState"));
        assertTrue(store.tx(()->store.all("task")).isEmpty());
        for(JsonNode account:initial.path("items")) {
            String accountId=text(account,"id");ObjectNode queued=store.tx(()->app.auth.check(accountId));assertEquals(queued,store.tx(()->app.auth.check(accountId)));
            JsonNode op=acquire("CHECK_ACCOUNT");assertEquals(accountId,text(op,"accountRef"));assertTrue(op.path("destination").isNull());assertEquals("AUTH",text(store.tx(()->store.get("task",text(op,"taskId"))),"kind"));
            if(text(account,"platform").equals("GOOGLE_DRIVE"))assertEquals("BILIBILI",text(op,"platform"));
            ObjectNode fact=(ObjectNode)account.deepCopy();fact.put("state","AUTH_REQUIRED");fact.put("reasonCode","ACCOUNT_NOT_CONFIGURED");fact.put("lastCheckedAt",clock.instant().toString());complete(op,object("account",fact));
            assertEquals("AUTH_REQUIRED",text(store.tx(()->app.auth.account(accountId)),"state"));
        }
        assertTrue(store.tx(()->store.all("order")).isEmpty());assertTrue(acquire("DOWNLOAD_MEDIA").isNull());assertTrue(acquire("SYNC_BIL_SOURCE").isNull());
    }
    @Test void identityAndSecretFieldsAreRejectedAndActiveNeedsACompletedCheck() {
        store.tx(()->app.auth.check("bilibili-main"));JsonNode op=acquire("CHECK_ACCOUNT");ObjectNode account=store.tx(()->app.auth.account("bilibili-main"));account.put("lastCheckedAt",clock.instant().toString());account.put("state","ACTIVE");
        ObjectNode wrong=account.deepCopy();wrong.put("id","bilibili-other");invalid(()->complete(op,object("account",wrong)));
        ObjectNode secret=account.deepCopy();secret.put("cookie","forbidden");invalid(()->complete(op,object("account",secret)));
        complete(op,object("account",account));assertEquals("ACTIVE",text(store.tx(()->app.auth.account("bilibili-main")),"state"));
        ObjectNode session=begin("BILIBILI");JsonNode auth=acquire("AUTH_STEP");complete(auth,facts(session,"SUCCEEDED","NONE",null,null));
        assertEquals("ACTIVE",text(store.tx(()->app.auth.account("bilibili-main")),"state"));assertTrue(store.tx(()->store.all("order")).isEmpty());
        assertThrows(Failure.class,()->store.tx(()->app.auth.check("bilibil-main")));
    }
    @Test void beginAndPollReuseOneFlowAndEachCompletedPollQueuesANewOperation() {
        ObjectNode first=begin("BILIBILI");assertEquals(first,begin("BILIBILI"));assertEquals(first,store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL"))));
        JsonNode begin=acquire("AUTH_STEP");assertEquals("BEGIN",text(begin.path("payload"),"action"));String qr="https://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=fixture-key&navhide=1";
        ObjectNode result=complete(begin,facts(first,"PENDING","SCAN_QR",qr,null));ObjectNode current=session(first);assertEquals(qr,text(current,"url"));assertFalse(text(current,"message").contains("provider text"));
        int count=store.tx(()->store.all("task").size());session(first);assertEquals(count,store.tx(()->store.all("task").size()).intValue());
        assertNotNull(store.tx(()->app.queue.result(text(begin,"operationId"),result,false)));
        ObjectNode polling=store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL")));JsonNode poll=acquire("AUTH_STEP");assertEquals("POLL",text(poll.path("payload"),"action"));assertNotEquals(text(begin,"operationId"),text(poll,"operationId"));assertEquals(text(begin.path("payload"),"authSessionId"),text(poll.path("payload"),"authSessionId"));
        complete(poll,facts(polling,"SUCCEEDED","NONE",null,null));assertEquals("SUCCEEDED",text(session(first),"state"));assertEquals("UNKNOWN",text(store.tx(()->app.auth.account("bilibili-main")),"state"));
        assertThrows(Failure.class,()->store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL"))));
        String persisted=store.tx(()->store.rows("SELECT body FROM events").stream().map(r->r[0]).reduce("",String::concat));assertFalse(persisted.contains("provider text"));assertFalse(persisted.contains("fixture-key"));
    }
    @Test void cancellationIsVersionedDurableAndLateSuccessCannotReviveOrChangeSavedAccount() throws Exception {
        ObjectNode first=begin("BILIBILI");JsonNode op=acquire("AUTH_STEP");
        assertThrows(Failure.class,()->store.tx(()->app.auth.cancel(text(first,"authSessionId"),"0")));
        ObjectNode cancelled=store.tx(()->app.auth.cancel(text(first,"authSessionId"),text(first,"revision")));assertEquals("CANCELLED",text(cancelled,"state"));assertEquals("NONE",text(cancelled,"nextAction"));
        complete(op,facts(first,"SUCCEEDED","NONE",null,null));assertEquals(cancelled,session(first));
        JsonNode cancel=acquire("AUTH_STEP");assertEquals("CANCEL",text(cancel.path("payload"),"action"));complete(cancel,facts(cancelled,"CANCELLED","NONE",null,null));
        store.close();start();assertEquals(cancelled,session(first));assertEquals("UNKNOWN",text(store.tx(()->app.auth.account("bilibili-main")),"state"));
        ObjectNode next=begin("BILIBILI");assertNotEquals(text(first,"authSessionId"),text(next,"authSessionId"));
    }
    @Test void expiryIsPureOnReadPersistsOnTickAndRejectsLateSuccess() {
        ObjectNode first=begin("BILIBILI");JsonNode op=acquire("AUTH_STEP");clock.now=clock.now.plusSeconds(601);
        ObjectNode projected=session(first);assertEquals("EXPIRED",text(projected,"state"));assertEquals("PENDING",text(store.tx(()->store.get("authSession",text(first,"authSessionId"))),"state"));
        assertThrows(Failure.class,()->store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL"))));
        store.tx(()->{app.auth.expire();return null;});assertEquals("EXPIRED",text(store.tx(()->store.get("authSession",text(first,"authSessionId"))),"state"));assertNotEquals(text(projected,"revision"),text(session(first),"revision"));
        ObjectNode result=request(op,facts(first,"SUCCEEDED","NONE",null,null));store.tx(()->app.queue.result(text(op,"operationId"),result,true));assertEquals("EXPIRED",text(session(first),"state"));
    }
    @Test void javaRestartResumesSamePersistedFlowAndLostResultsFailExplicitly() throws Exception {
        ObjectNode first=begin("GOOGLE_DRIVE");String task=text(first,"taskId");store.close();start();assertEquals(first,begin("GOOGLE_DRIVE"));assertEquals(task,text(session(first),"taskId"));
        JsonNode op=acquire("AUTH_STEP");assertEquals("BILIBILI",text(op,"platform"));assertEquals("drive-main",text(op,"accountRef"));assertEquals("GOOGLE_DRIVE",text(op.path("payload"),"platform"));
        ObjectNode failure=request(op,null);failure.put("status","RESULT_UNKNOWN");failure.set("error",object("code","RESULT_UNKNOWN","stage","AUTH_STEP","message","private provider text","completedEffects",List.of(),"pendingEffects",List.of()));
        store.tx(()->app.queue.result(text(op,"operationId"),failure,false));assertEquals("NEEDS_ACTION",text(session(first),"state"));assertEquals("NONE",text(session(first),"nextAction"));assertEquals("RESULT_UNKNOWN",text(session(first),"reasonCode"));
        assertEquals(text(first,"authSessionId"),text(begin("GOOGLE_DRIVE"),"authSessionId"));
        ObjectNode failed=session(first);assertThrows(Failure.class,()->store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL"))));store.tx(()->app.auth.cancel(text(first,"authSessionId"),text(failed,"revision")));
        assertNotEquals(text(first,"authSessionId"),text(begin("GOOGLE_DRIVE"),"authSessionId"));
    }
    @Test void providerUrlsMustBeOfficialForThatPlatformAndNeverContainCredentials() {
        ObjectNode session=begin("BILIBILI");JsonNode op=acquire("AUTH_STEP");
        for(String url:List.of("https://passport.bilibili.com.evil.invalid/h5-app/passport/login/scan?qrcode_key=x","http://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=x","https://passport.bilibili.com/h5-app/passport/login/scan?access_token=secret","https://user@passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=x","https://accounts.google.com/o/oauth2/v2/auth?client_id=x"))invalid(()->complete(op,facts(session,"PENDING","SCAN_QR",url,null)));
        ObjectNode wrong=facts(session,"PENDING","WAIT",null,null);((ObjectNode)wrong.path("session")).put("accountId","drive-main");invalid(()->complete(op,wrong));
        ObjectNode secret=facts(session,"PENDING","WAIT",null,null);((ObjectNode)secret.path("session")).put("refreshToken","secret");invalid(()->complete(op,secret));
        complete(op,facts(session,"PENDING","WAIT",null,null));
        ObjectNode drive=begin("GOOGLE_DRIVE");JsonNode driveOp=acquire("AUTH_STEP");String official="https://accounts.google.com/o/oauth2/v2/auth?client_id=fixture&redirect_uri=http%3A%2F%2F127.0.0.1%3A49123%2Fcallback&response_type=code&scope=drive.file&state=fixture";
        invalid(()->complete(driveOp,facts(drive,"PENDING","OPEN_URL",official.replace("127.0.0.1","evil.invalid"),null)));
        complete(driveOp,facts(drive,"PENDING","OPEN_URL",official,null));assertEquals(official,text(session(drive),"url"));
    }
    @Test void localSetupAndRequestBoundariesNeverAcceptBrowserSecrets() {
        for(String platform:List.of("TELEGRAM","QUARK","GOOGLE_DRIVE")) {
            ObjectNode first=begin(platform);JsonNode op=acquire("AUTH_STEP");complete(op,facts(first,"NEEDS_ACTION","LOCAL_SETUP",null,"ACCOUNT_NOT_CONFIGURED"));assertEquals("LOCAL_SETUP",text(session(first),"nextAction"));assertEquals(text(first,"authSessionId"),text(begin(platform),"authSessionId"));
        }
        assertThrows(Failure.class,()->store.tx(()->app.auth.begin(object("platform","BILIBILI","cookie","secret"))));
        ObjectNode first=begin("BILIBILI");assertThrows(Failure.class,()->store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL","code","secret"))));assertThrows(Failure.class,()->store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","IMPORT"))));
    }
    @Test void onlyBilibiliReadAndMediaOperationsDefaultToTheOneAccount() {
        store.tx(()->{for(String type:List.of("SYNC_BIL_SOURCE","FETCH_BIL_METADATA","DOWNLOAD_MEDIA"))app.queue.enqueue(type,"BILIBILI",type,null,"1",object());app.queue.enqueue("LIST_DRIVE_FOLDERS","BILIBILI","drive",null,"1",object("accountId","drive-main"));return null;});
        for(String capability:List.of("SYNC_BIL_SOURCE","FETCH_BIL_METADATA","DOWNLOAD_MEDIA"))assertEquals("bilibili-main",text(acquire(capability),"accountRef"));
        assertEquals("drive-main",text(acquire("LIST_DRIVE_FOLDERS"),"accountRef"));
    }
    @Test void explicitReadOnlyCheckRecoversFromNeedsActionAndOldObservationCannotOverwriteIt() {
        store.tx(()->app.auth.check("bilibili-main"));JsonNode old=acquire("CHECK_ACCOUNT");ObjectNode failure=request(old,null);failure.put("status","NEEDS_ACTION");failure.set("error",object("code","NETWORK_TEMPORARY","message","provider detail","completedEffects",List.of(),"pendingEffects",List.of()));
        store.tx(()->app.queue.result(text(old,"operationId"),failure,false));assertEquals("ERROR",text(store.tx(()->app.auth.account("bilibili-main")),"state"));
        store.tx(()->app.auth.check("bilibili-main"));JsonNode fresh=acquire("CHECK_ACCOUNT");assertNotEquals(text(old,"operationId"),text(fresh,"operationId"));
        ObjectNode active=store.tx(()->app.auth.account("bilibili-main"));active.put("state","ACTIVE");active.putNull("reasonCode");active.put("lastCheckedAt",clock.instant().toString());complete(fresh,object("account",active));
        ObjectNode obsolete=active.deepCopy();obsolete.put("state","AUTH_REQUIRED");obsolete.put("reasonCode","ACCOUNT_NOT_CONFIGURED");ObjectNode late=request(old,object("account",obsolete));store.tx(()->app.queue.result(text(old,"operationId"),late,true));
        assertEquals("ACTIVE",text(store.tx(()->app.auth.account("bilibili-main")),"state"));
    }
    @Test void driveSetupFailuresArePublicLocalSetupAndOptionalJavaFieldsAndNullExpiryAreAccepted() {
        for(String code:List.of("AUTH_CLIENT_REQUIRED","AUTH_SESSION_NOT_FOUND","AUTH_MANAGED_EXTERNALLY")) {
            ObjectNode first=begin("GOOGLE_DRIVE");JsonNode operation=acquire("AUTH_STEP");ObjectNode failure=request(operation,null);failure.put("status","NEEDS_ACTION");failure.set("error",object("code",code,"message","must not be published","completedEffects",List.of(),"pendingEffects",List.of()));
            store.tx(()->app.queue.result(text(operation,"operationId"),failure,false));ObjectNode setup=session(first);assertEquals("NEEDS_ACTION",text(setup,"state"));assertEquals("LOCAL_SETUP",text(setup,"nextAction"));assertEquals(code,text(setup,"reasonCode"));assertTrue(text(setup,"message").contains("Y7000P"));
            if(code.equals("AUTH_MANAGED_EXTERNALLY")) {assertEquals("Google Drive 由指定 rclone 配置管理，请在 Y7000P 完成该 remote 授权后主动检查",text(setup,"message"));assertFalse(text(setup,"message").contains("Desktop OAuth"));}
            else assertTrue(text(setup,"message").contains(code.equals("AUTH_CLIENT_REQUIRED")?"Desktop OAuth client":"未找到本次"));
            assertEquals(text(first,"authSessionId"),text(begin("GOOGLE_DRIVE"),"authSessionId"));
            store.tx(()->app.auth.poll(text(first,"authSessionId"),object("action","POLL")));JsonNode poll=acquire("AUTH_STEP");ObjectNode fact=facts(setup,"NEEDS_ACTION","NONE",null,code);((ObjectNode)fact.path("session")).putNull("expiresAt");complete(poll,fact);assertEquals("LOCAL_SETUP",text(session(first),"nextAction"));
            ObjectNode current=session(first);store.tx(()->app.auth.cancel(text(first,"authSessionId"),text(current,"revision")));JsonNode cancel=acquire("AUTH_STEP");complete(cancel,facts(current,"CANCELLED","NONE",null,null));
        }
        store.tx(()->app.auth.check("drive-main"));JsonNode check=acquire("CHECK_ACCOUNT");ObjectNode account=store.tx(()->app.auth.account("drive-main"));account.remove("revision");account.put("state","AUTH_REQUIRED");account.put("reasonCode","ACCOUNT_NOT_CONFIGURED");complete(check,object("account",account));assertTrue(store.tx(()->app.auth.account("drive-main")).path("revision").isTextual());assertTrue(store.tx(()->app.auth.account("drive-main")).path("lastCheckedAt").isNull());
    }
    static void invalid(Runnable work) {assertEquals("INVALID_INPUT",assertThrows(Failure.class,work::run).code);}
    static class MutableClock extends Clock {Instant now=Instant.parse("2026-10-16T00:00:00Z");public ZoneId getZone(){return ZoneOffset.UTC;}public Clock withZone(ZoneId zone){return this;}public Instant instant(){return now;}}
}
