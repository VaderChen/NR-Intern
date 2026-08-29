#import <Cocoa/Cocoa.h>

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
