// 原生 Smoke：建立獨立 WebView，開啟並取消選檔 sheet；不選取或讀取任何使用者檔案。
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <webview.h>
#include <stdio.h>

extern "C" int nr_install_file_picker(void *window_handle);

@interface NRSmokeOpenParameters : NSObject
@property BOOL allowsMultipleSelection;
@property BOOL allowsDirectories;
@end
@implementation NRSmokeOpenParameters
@end

static bool spinUntil(bool (^condition)(void)) {
  NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:8];
  while (!condition() && deadline.timeIntervalSinceNow > 0) {
    [NSRunLoop.currentRunLoop runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
  }
  return condition();
}

#define REQUIRE(value, message) do { if (!(value)) { fprintf(stderr, "%s\n", message); return 1; } } while (0)

int main() {
  @autoreleasepool {
    webview_t view = webview_create(0, nullptr);
    REQUIRE(view != nullptr, "無法建立 Smoke WebView");
    webview_set_title(view, "NR-Intern 原生選檔 Smoke（自動取消）");
    webview_set_size(view, 640, 400, WEBVIEW_HINT_NONE);
    NSWindow *window = (NSWindow *)webview_get_window(view);
    WKWebView *browser = (WKWebView *)window.contentView;
    @autoreleasepool {
      REQUIRE(nr_install_file_picker(window) == 1, "安裝原生選檔代理失敗");
    }
    REQUIRE(browser.UIDelegate != nil, "autorelease pool 結束後代理消失");
    REQUIRE([browser.UIDelegate respondsToSelector:@selector(webView:runOpenPanelWithParameters:initiatedByFrame:completionHandler:)], "缺少原生選檔回呼");
    id original = browser.UIDelegate;
    REQUIRE(nr_install_file_picker(window) == 1 && browser.UIDelegate == original, "重複安裝不應建立另一層代理");
    printf("代理生命週期與重複安裝：通過\n");

    for (int mode = 0; mode < 2; mode++) {
      NRSmokeOpenParameters *parameters = [[[NRSmokeOpenParameters alloc] init] autorelease];
      parameters.allowsMultipleSelection = mode == 1;
      parameters.allowsDirectories = mode == 1;
      __block int completed = 0;
      __block BOOL canceled = NO;
      // 此測試直接呼叫代理，沒有來源網頁 frame；正式回呼不使用這個參數。
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wnonnull"
      [browser.UIDelegate webView:browser
          runOpenPanelWithParameters:(WKOpenPanelParameters *)parameters
          initiatedByFrame:nil
          completionHandler:^(NSArray<NSURL *> *urls) { completed++; canceled = urls == nil; }];
#pragma clang diagnostic pop
      REQUIRE(spinUntil(^bool { return [window.attachedSheet isKindOfClass:NSOpenPanel.class]; }), "未開啟原生選檔 sheet");
      NSOpenPanel *panel = (NSOpenPanel *)window.attachedSheet;
      REQUIRE(panel.canChooseFiles && panel.allowsMultipleSelection == (mode == 1) && panel.canChooseDirectories == (mode == 1), "選檔參數不符");
      [panel cancel:nil];
      REQUIRE(spinUntil(^bool { return completed > 0 && window.attachedSheet == nil; }), "取消後未完成回呼");
      REQUIRE(completed == 1 && canceled, "取消必須恰好回呼一次 nil");
      printf("原生選檔 sheet（%s）開啟及取消：通過\n", mode == 0 ? "單選" : "多選／目錄");
    }
    webview_destroy(view);
  }
  return 0;
}
