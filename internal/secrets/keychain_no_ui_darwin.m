//go:build darwin && cgo

#import <LocalAuthentication/LocalAuthentication.h>

void *hideout_keychain_no_ui_context_create(void) {
	@autoreleasepool {
		LAContext *context = [[LAContext alloc] init];
		if (context == nil) {
			return NULL;
		}
		context.interactionNotAllowed = YES;
		return context;
	}
}

void hideout_keychain_no_ui_context_release(void *rawContext) {
	if (rawContext == NULL) {
		return;
	}
	[(LAContext *)rawContext release];
}
