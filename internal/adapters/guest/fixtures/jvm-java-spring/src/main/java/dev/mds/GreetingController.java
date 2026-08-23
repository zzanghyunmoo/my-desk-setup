package dev.mds;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class GreetingController {
    @Value("${mds.greeting}")
    private String greeting;

    @GetMapping("/greeting")
    public String greeting() {
        var inspected = greeting;
        return inspected;
    }
}
