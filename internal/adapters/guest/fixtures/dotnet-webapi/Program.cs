var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.MapGet("/greeting", () => new { message = "hello-api" });
if (Environment.GetEnvironmentVariable("MDS_CAPABILITY_PROBE") == "1")
{
    await app.StartAsync();
    var server = app.Services.GetRequiredService<Microsoft.AspNetCore.Hosting.Server.IServer>();
    var addresses = server.Features.Get<Microsoft.AspNetCore.Hosting.Server.Features.IServerAddressesFeature>();
    var address = addresses?.Addresses.Single() ?? throw new InvalidOperationException("ASP.NET probe address missing");
    using var client = new HttpClient();
    var body = await client.GetStringAsync($"{address}/greeting");
    if (!body.Contains("hello-api")) throw new InvalidOperationException("ASP.NET probe endpoint failed");
    Console.WriteLine("MDS_ASPNET_ENDPOINT_READY");
    await app.StopAsync();
    return;
}
app.Run();
