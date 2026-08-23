package dev.mds

import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication

@SpringBootApplication
class Application

fun main(args: Array<String>) {
    val context = runApplication<Application>(*args)
    if (System.getenv("MDS_CAPABILITY_PROBE") != "1") return
    val port = context.environment.getRequiredProperty("local.server.port")
    val request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:$port/greeting")).build()
    val response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString())
    check(response.statusCode() == 200 && response.body().contains("hello-kotlin")) {
        "Spring Boot Kotlin probe endpoint failed"
    }
    context.close()
}
