#import <Cocoa/Cocoa.h>

static NSWindow *nr_main_window = nil;
static id<NSWindowDelegate> nr_original_window_delegate = nil;
static NSObject<NSWindowDelegate> *nr_window_lifecycle_delegate = nil;
static NSStatusItem *nr_status_item = nil;
static BOOL nr_conversation_active = NO;

@interface NRWindowLifecycleDelegate : NSObject <NSWindowDelegate>
- (instancetype)initWithOriginalDelegate:(id<NSWindowDelegate>)delegate;
- (void)restoreWindow:(id)sender;
@end

@implementation NRWindowLifecycleDelegate {
  id<NSWindowDelegate> _originalDelegate;
}

- (instancetype)initWithOriginalDelegate:(id<NSWindowDelegate>)delegate {
  self = [super init];
  if (self != nil) {
    _originalDelegate = delegate;
  }
  return self;
}

- (BOOL)windowShouldClose:(NSWindow *)window {
  if (!nr_conversation_active) {
    if ([_originalDelegate respondsToSelector:@selector(windowShouldClose:)]) {
      return [_originalDelegate windowShouldClose:window];
    }
    return YES;
  }

  NSAlert *alert = [[NSAlert alloc] init];
  alert.alertStyle = NSAlertStyleWarning;
  alert.messageText = @"對話仍在進行";
  alert.informativeText = @"是否關閉 UI，並讓工作在背景繼續運作？";
  [alert addButtonWithTitle:@"是"];
  [alert addButtonWithTitle:@"否"];
  if ([alert runModal] != NSAlertFirstButtonReturn) {
    return NO;
  }

  [window orderOut:nil];
  if (nr_status_item == nil) {
    nr_status_item = [NSStatusBar.systemStatusBar statusItemWithLength:NSSquareStatusItemLength];
    NSStatusBarButton *button = nr_status_item.button;
    button.target = self;
    button.action = @selector(restoreWindow:);
    button.toolTip = @"重新開啟 NR-Intern";

    NSImage *source = NSApplication.sharedApplication.applicationIconImage;
    if (source != nil) {
      NSImage *icon = [source copy];
      icon.size = NSMakeSize(18, 18);
      button.image = icon;
    } else if (@available(macOS 11.0, *)) {
      button.image = [NSImage imageWithSystemSymbolName:@"bubble.left.and.bubble.right"
                              accessibilityDescription:@"NR-Intern"];
    }
  }
  [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyAccessory];
  return NO;
}

- (void)windowWillClose:(NSNotification *)notification {
  if ([_originalDelegate respondsToSelector:@selector(windowWillClose:)]) {
    [_originalDelegate windowWillClose:notification];
  }
  nr_main_window = nil;
}

- (void)restoreWindow:(id)sender {
  if (nr_main_window == nil) {
    return;
  }
  [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyRegular];
  [nr_main_window makeKeyAndOrderFront:nil];
  [NSApplication.sharedApplication activateIgnoringOtherApps:YES];
  if (nr_status_item != nil) {
    [NSStatusBar.systemStatusBar removeStatusItem:nr_status_item];
    nr_status_item = nil;
  }
}

- (BOOL)respondsToSelector:(SEL)selector {
  return [super respondsToSelector:selector] || [_originalDelegate respondsToSelector:selector];
}

- (id)forwardingTargetForSelector:(SEL)selector {
  if ([_originalDelegate respondsToSelector:selector]) {
    return _originalDelegate;
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

void nr_install_standard_menus(void) {
  NSApplication *application = NSApplication.sharedApplication;
  NSMenu *mainMenu = application.mainMenu;
  if (mainMenu == nil) {
    mainMenu = [[NSMenu alloc] initWithTitle:@""];
    application.mainMenu = mainMenu;
  }

  for (NSMenuItem *item in mainMenu.itemArray) {
    if ([item.submenu.title isEqualToString:@"編輯"]) {
      return;
    }
  }

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
  [editMenu addItem:NSMenuItem.separatorItem];
  [editMenu addItem:nr_menu_item(@"全選", @selector(selectAll:), @"a", command)];

  editRoot.submenu = editMenu;
  [mainMenu addItem:editRoot];
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
  nr_window_lifecycle_delegate = [[NRWindowLifecycleDelegate alloc]
      initWithOriginalDelegate:nr_original_window_delegate];
  window.delegate = nr_window_lifecycle_delegate;
}

void nr_set_conversation_active(int active) {
  nr_conversation_active = active != 0;
}

void nr_uninstall_window_lifecycle(void) {
  if (nr_main_window != nil && nr_main_window.delegate == nr_window_lifecycle_delegate) {
    nr_main_window.delegate = nr_original_window_delegate;
  }
  if (nr_status_item != nil) {
    [NSStatusBar.systemStatusBar removeStatusItem:nr_status_item];
    nr_status_item = nil;
  }
  [NSApplication.sharedApplication setActivationPolicy:NSApplicationActivationPolicyRegular];
  nr_conversation_active = NO;
  nr_main_window = nil;
  nr_original_window_delegate = nil;
  nr_window_lifecycle_delegate = nil;
}
