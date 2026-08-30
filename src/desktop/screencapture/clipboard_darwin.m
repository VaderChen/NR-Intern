#import <AppKit/AppKit.h>
#import <stdlib.h>
#import <string.h>

int nrInternCopyPNGToClipboard(const void *bytes, size_t length) {
  if (bytes == NULL || length == 0) {
    return 0;
  }
  @autoreleasepool {
    NSData *data = [NSData dataWithBytes:bytes length:length];
    NSPasteboard *pasteboard = [NSPasteboard generalPasteboard];
    [pasteboard clearContents];
    return [pasteboard setData:data forType:NSPasteboardTypePNG] ? 1 : 0;
  }
}

long long nrInternClipboardChangeCount(void) {
  @autoreleasepool {
    return (long long)NSPasteboard.generalPasteboard.changeCount;
  }
}

int nrInternReadPNGFromClipboard(void **bytes, size_t *length) {
  if (bytes == NULL || length == NULL) {
    return 0;
  }
  *bytes = NULL;
  *length = 0;
  @autoreleasepool {
    NSPasteboard *pasteboard = NSPasteboard.generalPasteboard;
    NSData *png = [pasteboard dataForType:NSPasteboardTypePNG];
    if (png.length == 0) {
      NSData *tiff = [pasteboard dataForType:NSPasteboardTypeTIFF];
      if (tiff.length > 0) {
        NSBitmapImageRep *representation = [NSBitmapImageRep imageRepWithData:tiff];
        png = [representation representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
      }
    }
    if (png.length == 0) {
      return 0;
    }
    void *copy = malloc(png.length);
    if (copy == NULL) {
      return 0;
    }
    memcpy(copy, png.bytes, png.length);
    *bytes = copy;
    *length = png.length;
    return 1;
  }
}

int nrInternScreenCaptureUIRunning(void) {
  @autoreleasepool {
    return [NSRunningApplication runningApplicationsWithBundleIdentifier:@"com.apple.screencaptureui"].count > 0 ? 1 : 0;
  }
}

void nrInternFreeCaptureMemory(void *value) {
  free(value);
}
