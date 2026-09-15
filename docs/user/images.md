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

After the model processes your message and responds, image data is automatically removed from the conversation history and replaced with a text placeholder like `[image: 2560x1545 png 478KB]`. This keeps the conversation context lean — a single image can cost 5K-160K tokens, and re-sending the same image across many turns compounds the cost quickly.

If you need the model to re-examine an image in a follow-up message, simply paste it again.

### Vision Capability

If your model doesn't support vision (configured with `vision: false`), images are automatically stripped before being sent to the model. The placeholder remains in your message history for reference.
