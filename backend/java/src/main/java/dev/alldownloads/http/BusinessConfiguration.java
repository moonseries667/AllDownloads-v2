package dev.alldownloads.http;

import dev.alldownloads.business.*;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.*;
import java.nio.file.Path;
import java.time.Clock;

@Configuration
public class BusinessConfiguration {
    @Bean(destroyMethod="close") BusinessStore businessStore(@Value("${alldownloads.database:runtime/control/business.db}") String database) throws Exception {return new BusinessStore(Path.of(database),Clock.systemUTC());}
    @Bean BusinessApplication businessApplication(BusinessStore store,@Value("${alldownloads.download-root:}") String root) {return new BusinessApplication(store,root);}
}
