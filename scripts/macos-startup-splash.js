ObjC.import("AppKit");
ObjC.import("Foundation");

function run(arguments) {
  if (arguments.length < 2) return;

  const signalPath = $(String(arguments[0]));
  const iconPath = $(String(arguments[1]));
  const appName = String(arguments[2] || "").trim() || "永不休息的實習生";
  const fileManager = $.NSFileManager.defaultManager;
  const application = $.NSApplication.sharedApplication;
  application.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

  const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
    $.NSMakeRect(0, 0, 360, 224),
    $.NSWindowStyleMaskBorderless,
    $.NSBackingStoreBuffered,
    false,
  );
  window.releasedWhenClosed = false;
  window.opaque = false;
  window.backgroundColor = $.NSColor.clearColor;
  window.hasShadow = true;
  window.level = $.NSFloatingWindowLevel;

  const content = $.NSView.alloc.initWithFrame($.NSMakeRect(0, 0, 360, 224));
  content.wantsLayer = true;
  content.layer.backgroundColor = $.NSColor.windowBackgroundColor.CGColor;
  content.layer.cornerRadius = 22;
  content.layer.borderWidth = 1;
  content.layer.borderColor = $.NSColor.separatorColor.CGColor;

  const icon = $.NSImage.alloc.initWithContentsOfFile(iconPath);
  const iconView = $.NSImageView.alloc.initWithFrame($.NSMakeRect(136, 116, 88, 88));
  iconView.image = icon;
  iconView.imageScaling = $.NSImageScaleProportionallyUpOrDown;
  content.addSubview(iconView);

  const title = $.NSTextField.labelWithString($(appName + " 啟動中"));
  title.setFont($.NSFont.systemFontOfSizeWeight(18, $.NSFontWeightSemibold));
  title.setTextColor($.NSColor.labelColor);
  title.sizeToFit;
  const titleWidth = Math.min(300, Math.ceil(Number(title.frame.size.width)));
  title.setFrame($.NSMakeRect((360 - titleWidth) / 2, 70, titleWidth, 28));
  title.setLineBreakMode($.NSLineBreakByTruncatingMiddle);
  content.addSubview(title);

  const progress = $.NSProgressIndicator.alloc.initWithFrame($.NSMakeRect(171, 24, 18, 18));
  progress.style = $.NSProgressIndicatorStyleSpinning;
  progress.controlSize = $.NSControlSizeSmall;
  progress.indeterminate = true;
  progress.startAnimation(null);
  content.addSubview(progress);

  window.contentView = content;
  window.center;
  window.makeKeyAndOrderFront(null);
  application.activateIgnoringOtherApps(true);

  const deadline = Date.now() + 120000;
  while (fileManager.fileExistsAtPath(signalPath) && Date.now() < deadline) {
    const data = $.NSData.dataWithContentsOfFile(signalPath);
    if (data && Number(data.length) > 0) break;
    $.NSRunLoop.currentRunLoop.runUntilDate($.NSDate.dateWithTimeIntervalSinceNow(0.05));
  }

  progress.stopAnimation(null);
  window.orderOut(null);
  if (fileManager.fileExistsAtPath(signalPath)) {
    fileManager.removeItemAtPathError(signalPath, null);
  }
}
