namespace Mds.Mvc.Pages;

public sealed class IndexModel : Microsoft.AspNetCore.Mvc.RazorPages.PageModel
{
    public string Greeting { get; private set; } = "hello-razor";
    public void OnGet() => Greeting = "hello-razor";
}
