public sealed class ProbeTests
{
    [Fact]
    public void GreetingIncludesTheInspectedName()
    {
        var name = "test";
        var actual = Probe.Greeting(name);
        Assert.Equal("hello-test", actual);
    }
}
