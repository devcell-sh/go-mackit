#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Runs block inside @try/@catch. Returns YES on success.
/// On ObjC exception, sets *outError to the exception description and returns NO.
BOOL ObjCTryCatch(void (NS_NOESCAPE ^block)(void),
                  NSString *_Nullable *_Nullable outError);

NS_ASSUME_NONNULL_END
