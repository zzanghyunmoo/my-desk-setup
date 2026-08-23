package dev.mds;

import static org.junit.jupiter.api.Assertions.assertEquals;
import org.junit.jupiter.api.Test;

class GreetingControllerTest {
    @Test
    void returnsConfiguredGreeting() throws Exception {
        var controller = new GreetingController();
        var field = GreetingController.class.getDeclaredField("greeting");
        field.setAccessible(true);
        field.set(controller, "hello-java");
        assertEquals("hello-java", controller.greeting());
    }

    @Test
    void breakpointProbe() throws Exception {
        var controller = new GreetingController();
        var field = GreetingController.class.getDeclaredField("greeting");
        field.setAccessible(true);
        field.set(controller, "debug-java-test");
        assertEquals("debug-java-test", controller.greeting());
    }
}
