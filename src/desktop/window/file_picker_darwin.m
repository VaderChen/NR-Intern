#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

// WKWebView 的 UIDelegate 是 weak；必須由應用程式持有，不能只放在 autorelease pool。
// 使用 WebView 的 associated object 管理生命週期，不建立跨視窗的全域代理。
@interface NRInternFilePickerDelegate : NSObject <WKUIDelegate> {
  id<WKUIDelegate> _original;
}
- (instancetype)initWithOriginal:(id<WKUIDelegate>)original;
@end

@implementation NRInternFilePickerDelegate
- (instancetype)initWithOriginal:(id<WKUIDelegate>)original {
  self = [super init];
  if (self) _original = [original retain];
  return self;
}

- (void)dealloc {
  [_original release];
  [super dealloc];
}

// 保留原本非選檔的 UI 回呼，避免影響未來 WebView 版本提供的其他功能。
- (BOOL)respondsToSelector:(SEL)selector {
  return [super respondsToSelector:selector] || [_original respondsToSelector:selector];
}

- (id)forwardingTargetForSelector:(SEL)selector {
  if ([_original respondsToSelector:selector]) return _original;
  return [super forwardingTargetForSelector:selector];
}

- (void)webView:(WKWebView *)webView
    runOpenPanelWithParameters:(WKOpenPanelParameters *)parameters
    initiatedByFrame:(WKFrameInfo *)frame
    completionHandler:(void (^)(NSArray<NSURL *> *))completionHandler {
  (void)frame;
  NSOpenPanel *panel = [NSOpenPanel openPanel];
  panel.canChooseFiles = YES;
  panel.canChooseDirectories = parameters.allowsDirectories;
  panel.allowsMultipleSelection = parameters.allowsMultipleSelection;
  panel.canCreateDirectories = NO;
  // 不指定起始路徑或自動選取檔案；取消以 nil 回傳 WebKit。
  void (^finished)(NSModalResponse) = ^(NSModalResponse response) {
    completionHandler(response == NSModalResponseOK ? panel.URLs : nil);
  };
  if (webView.window != nil) {
    [panel beginSheetModalForWindow:webView.window completionHandler:finished];
  } else {
    [panel beginWithCompletionHandler:finished];
  }
}
@end

static WKWebView *nr_find_web_view(NSView *view) {
  if ([view isKindOfClass:WKWebView.class]) return (WKWebView *)view;
  for (NSView *child in view.subviews) {
    WKWebView *found = nr_find_web_view(child);
    if (found != nil) return found;
  }
  return nil;
}

int nr_install_file_picker(void *window_handle) {
  if (window_handle == NULL || !NSThread.isMainThread) return 0;
  NSWindow *window = (NSWindow *)window_handle;
  WKWebView *webView = nr_find_web_view(window.contentView);
  if (webView == nil) return 0;
  if ([webView.UIDelegate isKindOfClass:NRInternFilePickerDelegate.class]) return 1;
  NRInternFilePickerDelegate *delegate = [[NRInternFilePickerDelegate alloc] initWithOriginal:webView.UIDelegate];
  static char ownerKey;
  objc_setAssociatedObject(webView, &ownerKey, delegate, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
  webView.UIDelegate = delegate;
  [delegate release];
  return 1;
}
