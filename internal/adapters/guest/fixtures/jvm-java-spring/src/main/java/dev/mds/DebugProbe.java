package dev.mds;

import java.lang.reflect.Field;

public class DebugProbe {
    public static void main(String[] args) throws Exception {
        var controller = new GreetingController();
        Field field = GreetingController.class.getDeclaredField("greeting");
        field.setAccessible(true);
        field.set(controller, "hello-java-debug");
        System.out.println(controller.greeting());
    }
}
