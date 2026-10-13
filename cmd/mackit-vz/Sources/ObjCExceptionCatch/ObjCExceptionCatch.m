#import "ObjCExceptionCatch.h"

BOOL ObjCTryCatch(void (NS_NOESCAPE ^block)(void),
                  NSString *__autoreleasing *outError) {
    @try {
        block();
        return YES;
    } @catch (NSException *e) {
        if (outError) {
            *outError = [e description];
        }
        return NO;
    }
}
