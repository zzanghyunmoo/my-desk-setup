package dev.mds

import org.springframework.beans.factory.annotation.Value
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

@RestController
class GreetingController(@Value("\${mds.greeting}") private val greeting: String) {
    @GetMapping("/greeting")
    fun greeting(): String {
        val inspected = greeting
        return inspected
    }
}
