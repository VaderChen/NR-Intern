#import <Cocoa/Cocoa.h>

static NSWindow *nr_main_window = nil;
static id<NSWindowDelegate> nr_original_window_delegate = nil;
static id<NSApplicationDelegate> nr_original_application_delegate = nil;
static NSObject<NSWindowDelegate, NSApplicationDelegate> *nr_window_lifecycle_delegate = nil;
static NSStatusItem *nr_status_item = nil;
static NSMenu *nr_status_menu = nil;
static BOOL nr_conversation_active = NO;
static NSString *nr_window_frame_name = @"NRInternMainWindowFrame.v1";

static void nr_save_main_window_frame(void) {
  if (nr_main_window != nil) {
    [nr_main_window saveFrameUsingName:nr_window_frame_name];
  }
}

static BOOL nr_window_titlebar_is_visible(NSWindow *window) {
  if (window == nil) {
    return NO;
  }
  NSRect frame = window.frame;
  CGFloat titlebarHeight = MIN(36.0, frame.size.height);
  NSRect titlebar = NSMakeRect(frame.origin.x,
                               NSMaxY(frame) - titlebarHeight,
                               frame.size.width,
                               titlebarHeight);
  for (NSScreen *screen in NSScreen.screens) {
    NSRect intersection = NSIntersectionRect(titlebar, screen.visibleFrame);
    if (!NSIsEmptyRect(intersection) && intersection.size.width >= 96.0 &&
        intersection.size.height >= 12.0) {
      return YES;
    }
  }
  return NO;
}

static void nr_move_window_to_visible_screen(NSWindow *window) {
  if (window == nil || nr_window_titlebar_is_visible(window)) {
    return;
  }
  NSScreen *screen = NSScreen.mainScreen;
  if (screen == nil && NSScreen.screens.count > 0) {
    screen = NSScreen.screens.firstObject;
  }
  if (screen == nil) {
    return;
  }
  NSRect frame = window.frame;
  NSRect visible = screen.visibleFrame;
  frame.size.width = MIN(frame.size.width, visible.size.width);
  frame.size.height = MIN(frame.size.height, visible.size.height);
  frame.origin.x = NSMidX(visible) - frame.size.width / 2.0;
  frame.origin.y = NSMidY(visible) - frame.size.height / 2.0;
  [window setFrame:frame display:NO];
}

static NSMenuItem *nr_status_menu_item(NSString *title, SEL action, id target) {
  NSMenuItem *item = [[[NSMenuItem alloc] initWithTitle:title
                                                action:action
                                         keyEquivalent:@""] autorelease];
  item.target = target;
  item.enabled = YES;
  return item;
}

static void nr_show_status_item(id target, SEL action) {
  if (nr_status_item == nil) {
    nr_status_item = [[NSStatusBar.systemStatusBar
        statusItemWithLength:NSSquareStatusItemLength] retain];
  }
  NSStatusBarButton *button = nr_status_item.button;
  if (button == nil) {
    return;
  }
  if (nr_status_menu == nil) {
    nr_status_menu = [[NSMenu alloc] initWithTitle:@"NR-Intern"];
    nr_status_menu.autoenablesItems = NO;
    [nr_status_menu addItem:nr_status_menu_item(@"顯示主視窗", action, target)];
    [nr_status_menu addItem:nr_status_menu_item(@"隱藏主視窗",
                                                NSSelectorFromString(@"hideWindow:"), target)];
    [nr_status_menu addItem:NSMenuItem.separatorItem];
    [nr_status_menu addItem:nr_status_menu_item(@"結束 NR-Intern",
                                                NSSelectorFromString(@"quitApplication:"), target)];
  }
  nr_status_item.menu = nr_status_menu;
  button.target = nil;
  button.action = nil;
  button.toolTip = @"重新開啟 NR-Intern";
  button.title = @"";

  NSImage *icon = nil;
  if (@available(macOS 11.0, *)) {
    icon = [NSImage imageWithSystemSymbolName:@"bubble.left.and.bubble.right.fill"
                     accessibilityDescription:@"NR-Intern 背景執行中"];
    if (icon == nil) {
      icon = [NSImage imageWithSystemSymbolName:@"bubble.left.and.bubble.right"
                       accessibilityDescription:@"NR-Intern 背景執行中"];
    }
  }
  if (icon != nil) {
    icon.template = YES;
    button.image = icon;
    button.imagePosition = NSImageOnly;
  } else {
    // 舊版 macOS 或 Symbol 載入失敗時仍需有可點擊且可辨識的項目。
    button.image = nil;
    button.title = @"NR";
    button.imagePosition = NSNoImage;
    nr_status_item.length = NSVariableStatusItemLength;
  }
  nr_status_item.visible = YES;
}

static void nr_remove_status_item(void) {
  if (nr_status_item == nil) {
    return;
  }
  nr_status_item.menu = nil;
  [NSStatusBar.systemStatusBar removeStatusItem:nr_status_item];
  [nr_status_item release];
  nr_status_item = nil;
  [nr_status_menu release];
  nr_status_menu = nil;
}

@interface NRWindowLifecycleDelegate : NSObject <NSWindowDelegate, NSApplicationDelegate>
- (instancetype)initWithOriginalWindowDelegate:(id<NSWindowDelegate>)windowDelegate
                    originalApplicationDelegate:(id<NSApplicationDelegate>)applicationDelegate;
- (void)restoreWindow:(id)sender;
- (void)hideWindow:(id)sender;
- (void)quitApplication:(id)sender;
@end

@implementation NRWindowLifecycleDelegate {
  id<NSWindowDelegate> _originalWindowDelegate;
  id<NSApplicationDelegate> _originalApplicationDelegate;
}

- (instancetype)initWithOriginalWindowDelegate:(id<NSWindowDelegate>)windowDelegate
                    originalApplicationDelegate:(id<NSApplicationDelegate>)applicationDelegate {
  self = [super init];
  if (self != nil) {
    _originalWindowDelegate = windowDelegate;
    _originalApplicationDelegate = applicationDelegate;
  }
  return self;
}

- (BOOL)windowShouldClose:(NSWindow *)window {
  if (!nr_conversation_active) {
    if ([_originalWindowDelegate respondsToSelector:@selector(windowShouldClose:)]) {
      return [_originalWindowDelegate windowShouldClose:window];
    }
    return YES;
  }

  NSAlert *alert = [[NSAlert alloc] init];
  alert.alertStyle = NSAlertStyleWarning;
  alert.messageText = @"對話仍在進行";
  alert.informativeText = @"是否關閉 UI，並讓工作在背景繼續運作？";
  [alert addButtonWithTitle:@"是"];
  [alert addButtonWithTitle:@"否"];
  NSInteger response = [alert runModal];
  [alert release];
  if (response != NSAlertFirstButtonReturn) {
    return NO;
  }

  [self hideWindow:nil];
  return NO;
}

- (void)windowWillClose:(NSNotification *)notification {
  nr_save_main_window_frame();
  if ([_originalWindowDelegate respondsToSelector:@selector(windowWillClose:)]) {
    [_originalWindowDelegate windowWillClose:notification];
  }
  nr_main_window = nil;
}

- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender
                    hasVisibleWindows:(BOOL)hasVisibleWindows {
  if (nr_main_window != nil && !nr_main_window.visible) {
    [self restoreWindow:nil];
    return YES;
  }
  if ([_originalApplicationDelegate
          respondsToSelector:@selector(applicationShouldHandleReopen:hasVisibleWindows:)]) {
    return [_originalApplicationDelegate applicationShouldHandleReopen:sender
                                                      hasVisibleWindows:hasVisibleWindows];
  }
  return YES;
}

- (void)restoreWindow:(id)sender {
  if (nr_main_window == nil) {
    return;
  }
  [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyRegular];
  [nr_main_window makeKeyAndOrderFront:nil];
  [NSApplication.sharedApplication activateIgnoringOtherApps:YES];
}

- (void)hideWindow:(id)sender {
  if (nr_main_window == nil) {
    return;
  }
  nr_save_main_window_frame();
  nr_show_status_item(self, @selector(restoreWindow:));
  [nr_main_window orderOut:nil];
  [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyAccessory];
  // 切換 ActivationPolicy 可能重建選單列；再套用一次可確保狀態項目可見。
  nr_show_status_item(self, @selector(restoreWindow:));
}

- (void)quitApplication:(id)sender {
  if (nr_conversation_active) {
    NSAlert *alert = [[NSAlert alloc] init];
    alert.alertStyle = NSAlertStyleWarning;
    alert.messageText = @"對話仍在進行";
    alert.informativeText = @"結束 NR-Intern 會停止目前的背景工作。";
    [alert addButtonWithTitle:@"繼續背景執行"];
    [alert addButtonWithTitle:@"結束 NR-Intern"];
    NSInteger response = [alert runModal];
    [alert release];
    if (response != NSAlertSecondButtonReturn) {
      return;
    }
  }
  [NSApplication.sharedApplication terminate:nil];
}

- (BOOL)respondsToSelector:(SEL)selector {
  return [super respondsToSelector:selector] ||
         [_originalWindowDelegate respondsToSelector:selector] ||
         [_originalApplicationDelegate respondsToSelector:selector];
}

- (id)forwardingTargetForSelector:(SEL)selector {
  if ([_originalWindowDelegate respondsToSelector:selector]) {
    return _originalWindowDelegate;
  }
  if ([_originalApplicationDelegate respondsToSelector:selector]) {
    return _originalApplicationDelegate;
  }
  return [super forwardingTargetForSelector:selector];
}

@end

static NSMenuItem *nr_menu_item(NSString *title, SEL action, NSString *key,
                                NSEventModifierFlags modifiers) {
  NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:title
                                               action:action
                                        keyEquivalent:key];
  item.target = nil;
  item.keyEquivalentModifierMask = modifiers;
  return item;
}

// nr_menu_contains_action 以 selector 判斷選單是否已經有某個標準動作，
// 比對標題會因為語言或 webview 版本不同而失準。
static BOOL nr_menu_contains_action(NSMenu *menu, SEL action) {
  for (NSMenuItem *item in menu.itemArray) {
    if (item.action == action) {
      return YES;
    }
    if (item.submenu != nil && nr_menu_contains_action(item.submenu, action)) {
      return YES;
    }
  }
  return NO;
}

// nr_install_application_menu 補上 App 選單。WKWebView 容器不會自己建立它，
// 少了這個選單，⌘Q 結束、⌘H 隱藏這些使用者視為理所當然的系統快捷鍵全都沒有作用。
static void nr_install_application_menu(NSMenu *mainMenu) {
  if (nr_menu_contains_action(mainMenu, @selector(terminate:))) {
    return;
  }
  NSString *name = NSRunningApplication.currentApplication.localizedName;
  if (name.length == 0) {
    name = @"NR-Intern";
  }
  const NSEventModifierFlags command = NSEventModifierFlagCommand;
  NSMenuItem *root = [[NSMenuItem alloc] initWithTitle:name action:nil keyEquivalent:@""];
  NSMenu *menu = [[NSMenu alloc] initWithTitle:name];
  [menu addItem:nr_menu_item([@"關於 " stringByAppendingString:name],
                             @selector(orderFrontStandardAboutPanel:), @"",
                             0)];
  [menu addItem:NSMenuItem.separatorItem];
  [menu addItem:nr_menu_item([@"隱藏 " stringByAppendingString:name], @selector(hide:), @"h", command)];
  [menu addItem:nr_menu_item(@"隱藏其他", @selector(hideOtherApplications:), @"h",
                             command | NSEventModifierFlagOption)];
  [menu addItem:nr_menu_item(@"顯示全部", @selector(unhideAllApplications:), @"", 0)];
  [menu addItem:NSMenuItem.separatorItem];
  [menu addItem:nr_menu_item([@"結束 " stringByAppendingString:name], @selector(terminate:), @"q", command)];
  root.submenu = menu;
  [mainMenu insertItem:root atIndex:0];
}

// nr_install_window_menu 補上視窗選單：⌘M 最小化與 ⌘W 關閉同樣是系統層級的
// 標準快捷鍵。⌘W 會走既有的 windowShouldClose: 流程，工作進行中仍只是隱藏視窗。
static void nr_install_window_menu(NSMenu *mainMenu) {
  if (nr_menu_contains_action(mainMenu, @selector(performMiniaturize:))) {
    return;
  }
  const NSEventModifierFlags command = NSEventModifierFlagCommand;
  NSMenuItem *root = [[NSMenuItem alloc] initWithTitle:@"視窗" action:nil keyEquivalent:@""];
  NSMenu *menu = [[NSMenu alloc] initWithTitle:@"視窗"];
  [menu addItem:nr_menu_item(@"最小化", @selector(performMiniaturize:), @"m", command)];
  [menu addItem:nr_menu_item(@"縮放", @selector(performZoom:), @"", 0)];
  [menu addItem:NSMenuItem.separatorItem];
  [menu addItem:nr_menu_item(@"關閉視窗", @selector(performClose:), @"w", command)];
  root.submenu = menu;
  [mainMenu addItem:root];
  NSApplication.sharedApplication.windowsMenu = menu;
}

void nr_install_standard_menus(void) {
  NSApplication *application = NSApplication.sharedApplication;
  NSMenu *mainMenu = application.mainMenu;
  if (mainMenu == nil) {
    mainMenu = [[NSMenu alloc] initWithTitle:@""];
    application.mainMenu = mainMenu;
  }

  nr_install_application_menu(mainMenu);

  if (!nr_menu_contains_action(mainMenu, @selector(paste:))) {
    NSMenuItem *editRoot = [[NSMenuItem alloc] initWithTitle:@"編輯"
                                                     action:nil
                                              keyEquivalent:@""];
    NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"編輯"];
    const NSEventModifierFlags command = NSEventModifierFlagCommand;

    [editMenu addItem:nr_menu_item(@"復原", @selector(undo:), @"z", command)];
    [editMenu addItem:nr_menu_item(@"重做", @selector(redo:), @"z",
                                   command | NSEventModifierFlagShift)];
    [editMenu addItem:NSMenuItem.separatorItem];
    [editMenu addItem:nr_menu_item(@"剪下", @selector(cut:), @"x", command)];
    [editMenu addItem:nr_menu_item(@"複製", @selector(copy:), @"c", command)];
    [editMenu addItem:nr_menu_item(@"貼上", @selector(paste:), @"v", command)];
    [editMenu addItem:nr_menu_item(@"貼上並符合樣式", @selector(pasteAsPlainText:), @"v",
                                   command | NSEventModifierFlagOption |
                                       NSEventModifierFlagShift)];
    [editMenu addItem:NSMenuItem.separatorItem];
    [editMenu addItem:nr_menu_item(@"全選", @selector(selectAll:), @"a", command)];

    editRoot.submenu = editMenu;
    [mainMenu addItem:editRoot];
  }

  nr_install_window_menu(mainMenu);
}

void nr_activate_application(void) {
  [NSApplication.sharedApplication activateIgnoringOtherApps:YES];
}

void nr_install_window_lifecycle(void *window_handle) {
  NSWindow *window = (NSWindow *)window_handle;
  if (window == nil) {
    return;
  }
  nr_main_window = window;
  nr_original_window_delegate = window.delegate;
  nr_original_application_delegate = NSApplication.sharedApplication.delegate;
  nr_window_lifecycle_delegate = [[NRWindowLifecycleDelegate alloc]
      initWithOriginalWindowDelegate:nr_original_window_delegate
          originalApplicationDelegate:nr_original_application_delegate];
  window.delegate = nr_window_lifecycle_delegate;
  NSApplication.sharedApplication.delegate = nr_window_lifecycle_delegate;
  nr_show_status_item(nr_window_lifecycle_delegate, @selector(restoreWindow:));
}

void nr_restore_window_frame(void *window_handle) {
  NSWindow *window = (NSWindow *)window_handle;
  if (window == nil) {
    return;
  }
  [window setFrameAutosaveName:nr_window_frame_name];
  [window setFrameUsingName:nr_window_frame_name];
  nr_move_window_to_visible_screen(window);
}

void nr_set_conversation_active(int active) {
  nr_conversation_active = active != 0;
}

void nr_restore_application_window(void) {
  if (nr_window_lifecycle_delegate != nil) {
    [(NRWindowLifecycleDelegate *)nr_window_lifecycle_delegate restoreWindow:nil];
    return;
  }
  if (nr_main_window != nil) {
    [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyRegular];
    [nr_main_window makeKeyAndOrderFront:nil];
    [NSApplication.sharedApplication activateIgnoringOtherApps:YES];
  }
}

void nr_hide_application_window(void) {
  if (nr_window_lifecycle_delegate != nil) {
    [(NRWindowLifecycleDelegate *)nr_window_lifecycle_delegate hideWindow:nil];
    return;
  }
  if (nr_main_window != nil) {
    [nr_main_window orderOut:nil];
  }
}

void nr_uninstall_window_lifecycle(void) {
  nr_save_main_window_frame();
  if (nr_main_window != nil && nr_main_window.delegate == nr_window_lifecycle_delegate) {
    nr_main_window.delegate = nr_original_window_delegate;
  }
  if (NSApplication.sharedApplication.delegate == nr_window_lifecycle_delegate) {
    NSApplication.sharedApplication.delegate = nr_original_application_delegate;
  }
  nr_remove_status_item();
  [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyRegular];
  nr_conversation_active = NO;
  nr_main_window = nil;
  nr_original_window_delegate = nil;
  nr_original_application_delegate = nil;
  nr_window_lifecycle_delegate = nil;
}
