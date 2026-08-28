//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

char* nr_dropped_folders_json(void) {
  @autoreleasepool {
    NSPasteboard *pasteboard = [NSPasteboard pasteboardWithName:NSPasteboardNameDrag];
    NSDictionary *options = @{ NSPasteboardURLReadingFileURLsOnlyKey: @YES };
    NSArray<NSURL *> *urls = [pasteboard readObjectsForClasses:@[[NSURL class]] options:options];
    NSMutableArray<NSString *> *paths = [NSMutableArray array];
    for (NSURL *url in urls) {
      NSNumber *isDirectory = nil;
      if (![url getResourceValue:&isDirectory forKey:NSURLIsDirectoryKey error:nil] || !isDirectory.boolValue) {
        continue;
      }
      if (url.path.length > 0 && ![paths containsObject:url.path]) {
        [paths addObject:url.path];
      }
    }
    NSData *data = [NSJSONSerialization dataWithJSONObject:paths options:0 error:nil];
    if (data == nil) {
      return NULL;
    }
    NSString *json = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    return json == nil ? NULL : strdup(json.UTF8String);
  }
}
