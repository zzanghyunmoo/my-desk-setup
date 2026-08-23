package dev.mds

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test

class GreetingControllerTest {
    @Test
    fun returnsConfiguredGreeting() {
        assertEquals("hello-kotlin", GreetingController("hello-kotlin").greeting())
    }

    @Test
    fun breakpointProbe() {
        val controller = GreetingController("debug-kotlin-test")
        assertEquals("debug-kotlin-test", controller.greeting())
    }
}
