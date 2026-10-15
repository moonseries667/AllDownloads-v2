package dev.alldownloads.http;

import dev.alldownloads.business.BusinessApplication;
import org.springframework.context.annotation.Configuration;
import org.springframework.scheduling.annotation.EnableScheduling;
import org.springframework.scheduling.annotation.Scheduled;

/** Uses the existing durable queue; disabled/new accounts never trigger platform requests. */
@Configuration
@EnableScheduling
public class SavedWatcher {
    private final BusinessApplication app;
    public SavedWatcher(BusinessApplication app){this.app=app;}
    @Scheduled(fixedDelay=5000,initialDelay=5000) public void check(){app.feedback.tick();}
}
