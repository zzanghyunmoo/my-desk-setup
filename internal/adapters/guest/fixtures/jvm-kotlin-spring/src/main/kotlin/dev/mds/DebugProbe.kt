package dev.mds

fun main() {
    val controller = GreetingController("hello-kotlin-debug")
    val inspected = controller.greeting()
    printResult(inspected)
}

private fun printResult(value: String) {
    val rendered = value.uppercase()
    println(rendered)
}
