package dev.mds;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.ConfigurableApplicationContext;

@SpringBootApplication
public class Application {
    public static void main(String[] args) throws Exception {
        ConfigurableApplicationContext context = SpringApplication.run(Application.class, args);
        if (!"1".equals(System.getenv("MDS_CAPABILITY_PROBE"))) {
            return;
        }
        int port = Integer.parseInt(context.getEnvironment().getRequiredProperty("local.server.port"));
        var request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/greeting")).build();
        var response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() != 200 || !response.body().contains("hello-java")) {
            throw new IllegalStateException("Spring Boot Java probe endpoint failed");
        }
        SpringApplication.exit(context);
    }
}
