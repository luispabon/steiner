# Image Paste Feature

How to use images in steiner, and how steiner manages images in conversation context.

## Using Image Paste

In the interactive TUI, use **Ctrl+V** to paste an image. Steiner reads the image from your clipboard or from a file path. Images are automatically:

- **Resized** to a maximum of 2048px on the longest side to keep token costs manageable
- **Token-accounted** using the formula `(width × height) / 750 + 85` overhead per image
- **Displayed** as `[Image N]` in the composer until you submit

You can paste multiple images before submitting — they accumulate and are all sent with your message. Use `/clear` in the TUI to dismiss pending images without sending them.

**Supported formats:** PNG, JPG, JPEG, GIF, WebP
**Max size:** 5MB per image

### What Happens After the Model Responds

After the model processes your message and responds, image data is automatically removed from the conversation history and replaced with a text placeholder like `[image img-3: /path/to/image.png 800x600 png 245KB — …]`. This keeps the conversation context lean — a single image can cost 5K-160K tokens, and re-sending the same image across many turns compounds the cost quickly. Image data is also stripped whenever a run ends, including on errors and cancellation, so session files never contain image bytes.

### Where Images Live

Images are stored per conversation under `.steiner/tmp/images/\u003csession id\u003e/`, together with an `index.json` that records their IDs, file paths, and metadata. These folders persist across restarts, so a resumed session can re-examine its images: use the `read` tool on the placeholder's file path, or the `vision` sub-agent with the placeholder's `image_id`. Image IDs continue per session, so a resumed conversation never reuses an ID that already appears in its history.

Folders unused for 30 days are pruned at startup. Non-interactive runs (`exec`, `oneshot`) use an ephemeral per-process folder that is removed on exit. Deleting or evicting a session does not delete its image folder; it is pruned after 30 days. Sessions created before this layout have no index, but the placeholder IDs in their history still raise the ID floor, so new images never collide with them. A session not resumed for 30 days loses its images, and vision recall then reports that the image is no longer available and asks you to paste it again.

### Vision Capability

If your model doesn't support vision (configured with `vision: false`), images are automatically stripped before being sent to the model. The placeholder remains in your message history for reference.
