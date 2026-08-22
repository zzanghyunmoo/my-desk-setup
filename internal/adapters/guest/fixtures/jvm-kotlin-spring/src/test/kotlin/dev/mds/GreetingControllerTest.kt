package dev.mds

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test

class GreetingControllerTest {
    @Test
    fun returnsConfiguredGreeting() {
        assertEquals("hello-kotlin", GreetingController("hello-kotlin").greeting())
    }
}
