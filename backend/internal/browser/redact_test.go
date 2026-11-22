package browser

import "testing"

// What the steers hand back must never be the password, whichever way the
// portal encodes it. Both bodies below are invented.
func TestARedactedBodyKeepsTheNamesAndDropsTheValues(t *testing.T) {
	for _, one := range []struct{ name, body, want string }{
		{
			name: "a form post",
			body: "username=someone%40example.test&password=invented&rememberMe=true",
			want: "username=someone%40example.test&password=[removed]&rememberMe=true",
		},
		{
			name: "a form post whose field is named around the word",
			body: "user_password_confirm=invented&otpCode=123456&zip=12345",
			want: "user_password_confirm=[removed]&otpCode=[removed]&zip=12345",
		},
		{
			name: "json",
			body: `{"username":"someone@example.test","password":"invented","keep":true}`,
			want: `{"username":"someone@example.test","password":"[removed]","keep":true}`,
		},
		{
			name: "a token in an answer",
			body: `{"access_token":"abc.def","expires_in":3600}`,
			want: `{"access_token":"[removed]","expires_in":3600}`,
		},
		{
			name: "a session handed over in the query",
			body: "transferKey=opaque&userRole=Inquiry",
			want: "transferKey=[removed]&userRole=Inquiry",
		},
		{
			name: "form keys with dots, brackets and prefixes",
			body: "Input.Password=invented&login%5Bpassword%5D=invented&ctl00%24txtPassword=invented&pwd=invented&Input.Email=someone%40example.test",
			want: "Input.Password=[removed]&login%5Bpassword%5D=[removed]&ctl00%24txtPassword=[removed]&pwd=[removed]&Input.Email=someone%40example.test",
		},
		{
			name: "json with a numeric code and keys the old pattern missed",
			body: `{"otp":123456,"user.Password":"in\"vented","mfa-code": 654321,"amount":12.5}`,
			want: `{"otp":"[removed]","user.Password":"[removed]","mfa-code": "[removed]","amount":12.5}`,
		},
		{
			name: "a json body is not read as one form field",
			body: `{"token":"abc=","next":"a&b"}`,
			want: `{"token":"[removed]","next":"a&b"}`,
		},
		{
			name: "nothing worth taking out",
			body: "accountId=1&policy=2",
			want: "accountId=1&policy=2",
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			if got := Redacted(one.body); got != one.want {
				t.Fatalf("Redacted(%q) = %q, want %q", one.body, got, one.want)
			}
		})
	}
	if got := Redacted(""); got != "" {
		t.Fatalf("an empty body stays empty, got %q", got)
	}
}
