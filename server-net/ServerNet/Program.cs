using System.Diagnostics;
using System.Net.WebSockets;

var builder = WebApplication.CreateBuilder(args);

// Add services to the container.

var app = builder.Build();

// Configure the HTTP request pipeline.
var semaphore = new SemaphoreSlim(1, 1);
var clients = new Dictionary<WebSocket, bool>();
app.UseWebSockets();

async Task Receive(WebSocket ws)
{
    var buffer = new byte[1024];
    while (ws.State == WebSocketState.Open)
    {
        var receiveRes = await ws.ReceiveAsync(new ArraySegment<byte>(buffer), CancellationToken.None);
        if (receiveRes.CloseStatus == null)
        {
            var msg = System.Text.Encoding.UTF8.GetString(buffer, 0, receiveRes.Count);
            Console.WriteLine($"client send a message: {msg}");
        }
        else
        {
            await Remove(ws);
            Console.WriteLine($"closed ws: {receiveRes.CloseStatusDescription}/{receiveRes.CloseStatus}");
            return;
        }
    }
}

async Task SendAll(string msg)
{
    var bytes = System.Text.Encoding.UTF8.GetBytes(msg);
    var failedClients = new List<WebSocket>();
    foreach (var client in clients.Keys)
    {
        try
        {
            await client.SendAsync(bytes, WebSocketMessageType.Text, true, CancellationToken.None);
        }
        catch (Exception e)
        {
            Console.WriteLine($"failed to send message to client: {e}");
            failedClients.Add(client);
        }
    }

    if (failedClients.Count > 0)
    {
        foreach (var failedClient in failedClients)
        {
            clients.Remove(failedClient);

            // fire and forget
            TryClose(failedClient);
        }
    }
}

async void TryClose(WebSocket ws)
{
    try
    {
        await ws.CloseAsync(WebSocketCloseStatus.ProtocolError, "error", CancellationToken.None);
    }
    catch
    {
        // Do nothing
    }
}

async Task Remove(WebSocket ws)
{
    try
    {
        await semaphore.WaitAsync();
        clients.Remove(ws);
    }
    finally
    {
        semaphore.Release();
    }
}

app.Use(async (context, next) =>
{
    if (context.Request.Path == "/ws")
    {
        if (context.WebSockets.IsWebSocketRequest)
        {
            var ws = await context.WebSockets.AcceptWebSocketAsync();
            try
            {
                await semaphore.WaitAsync();
                clients.Add(ws, true);
            }
            finally
            {
                semaphore.Release();
            }

            await Receive(ws);
        }
        else
        {
            context.Response.StatusCode = StatusCodes.Status400BadRequest;
        }
    }
    else
    {
        await next(context);
    }
});

app.MapGet("/clients", async (r) =>
{
    try
    {
        await semaphore.WaitAsync();
        var str = $"clients: {clients.Count}";
        r.Response.StatusCode = StatusCodes.Status200OK;
        r.Response.ContentType = "text/plain";
        await r.Response.Body.WriteAsync(System.Text.Encoding.UTF8.GetBytes(str));
    }
    finally
    {
        semaphore.Release();
    }
});

app.MapPost("/push", async (r) =>
{
    var start = Stopwatch.StartNew();
    try
    {
        await semaphore.WaitAsync();
        string content;
        using (var reader = new StreamReader(r.Request.Body))
        {
            content = await reader.ReadToEndAsync();
        }

        await SendAll(content);
    }
    finally
    {
        semaphore.Release();
        var elapsed = start.ElapsedMilliseconds;
        app.Logger.LogInformation($"push done in {elapsed} ms");
    }
});

app.Run();