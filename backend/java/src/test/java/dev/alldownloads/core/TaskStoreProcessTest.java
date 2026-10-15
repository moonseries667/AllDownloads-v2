package dev.alldownloads.core;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.concurrent.*;

import static dev.alldownloads.core.Model.*;
import static org.junit.jupiter.api.Assertions.*;

class TaskStoreProcessTest {
    @TempDir Path temp;

    @Test void forcedChildTerminationPreservesCommitAndReleasesOwnerLock() throws Exception {
        Path database = temp.resolve("process.sqlite");
        String java = Path.of(System.getProperty("java.home"), "bin",
                System.getProperty("os.name").toLowerCase().contains("win") ? "java.exe" : "java").toString();
        String classPath = System.getProperty("surefire.test.class.path", System.getProperty("java.class.path"));
        Process child = new ProcessBuilder(java, "-cp", classPath, Child.class.getName(), database.toString())
                .redirectErrorStream(true).start();
        ExecutorService reader = Executors.newSingleThreadExecutor();
        Future<String> ready = reader.submit(() -> {
            try (BufferedReader input = new BufferedReader(
                    new InputStreamReader(child.getInputStream(), StandardCharsets.UTF_8))) {
                return input.readLine();
            }
        });
        try {
            String line;
            try { line = ready.get(15, TimeUnit.SECONDS); }
            catch (TimeoutException timeout) {
                throw new AssertionError("Child did not report a committed task before timeout", timeout);
            }
            assertNotNull(line);
            assertTrue(line.startsWith("READY:"), line);
            String id = line.substring("READY:".length());
            assertTrue(child.isAlive());
            child.destroyForcibly();
            assertTrue(child.waitFor(5, TimeUnit.SECONDS));
            try (TaskStore reopened = reopenAfterTermination(database)) {
                Task restored = reopened.get(id);
                assertEquals(Status.QUEUED, restored.status());
                assertEquals("process-storage-revision", restored.request().storageRevision());
            }
        } finally {
            if (child.isAlive()) { child.destroyForcibly(); child.waitFor(5, TimeUnit.SECONDS); }
            reader.shutdownNow();
        }
    }

    private static TaskStore reopenAfterTermination(Path database) throws Exception {
        // Windows may signal the terminated process before releasing its file locks.
        // Verify eventual release within a fixed deadline; every other I/O failure remains fatal.
        long deadline=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
        while(true) {
            try {return new TaskStore(database,Clock.fixed(Instant.parse("2026-10-15T00:00:00Z"),ZoneOffset.UTC));}
            catch(IOException e) {
                if(!"Control directory is already in use".equals(e.getMessage())||System.nanoTime()>=deadline)throw e;
                Thread.sleep(25);
            }
        }
    }

    public static final class Child {
        private Child() {}
        public static void main(String[] args) throws Exception {
            try (TaskStore store = new TaskStore(Path.of(args[0]), Clock.systemUTC())) {
                Task task = store.create("child-key", new Request("fixture:child", "process/committed.txt",
                        "committed before ready", "process-storage-revision"));
                System.out.println("READY:" + task.id());
                System.out.flush();
                new CountDownLatch(1).await();
            }
        }
    }
}
