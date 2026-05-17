# dbrain Chrome Extension

This unpacked Chrome extension adds the current tab URL to dbrain through the
existing `POST /api/links` web API. The JavaScript uses the WebExtensions API
shape that Safari can also package through Xcode.

## Install

1. Open `chrome://extensions`.
2. Enable **Developer mode**.
3. Click **Load unpacked**.
4. Select this directory: `browser/chrome-extension`.
5. Open the extension details and pin **dbrain Link Saver** to the toolbar.

## Configure

1. Open the extension options.
2. Set the dbrain web base URL, for example:
   - `http://127.0.0.1:8742`
   - `https://dbrain.example.ts.net`
3. Click **Save** and approve the Chrome host permission prompt.
4. If web auth is enabled, log in to dbrain in the same Chrome profile.

## Use

Click the toolbar button on any `http://` or `https://` page. The extension sends
this request:

```json
{
  "url": "https://example.com/page",
  "enrich": false
}
```

Successful saves show an `OK` badge briefly. Authentication failures open the
dbrain login page; log in and click the toolbar button again.
