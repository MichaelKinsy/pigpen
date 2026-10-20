package warden

import (
	"reflect"
	"strings"
	"testing"
)

func eqStrings(t *testing.T, got, want []string, msg string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %q, want %q", msg, got, want)
	}
}

// Pieces split so repository secret scanners do not read a fixture as a live key (as upstream does).
func join(parts ...string) string { return strings.Join(parts, "") }

// twin: tests/credential-shapes.test.ts:5
func TestPlaceholdersPlainNumbersAndCodeIdentifiersAreNotCredentials(t *testing.T) {
	samples := [][2]string{
		{"sed output of a redacted env file", "DATABASE_URL=<redacted>\nTYPESAFE_API_KEY=<redacted>\nGITHUB_TOKEN=<redacted>"},
		{"JSON token counts", `{"model": "jev-latest", "promptTokens": 14350, "cacheReadTokens": 19415506, "cacheWriteTokens": 797330}`},
		{"grep of source lines that handle secrets", strings.Join([]string{
			"src/extension.ts:1359:    const secretValues = output.secretIds ?? (output.secret && output.secretId !== undefined ? [output.secretId] : []);",
			"src/extension.ts:1361:    const unseenSecrets = secretValues.filter((id) => !secretsSeen.has(id));",
			"src/load.ts:151:  const secrets = findSecrets(body);",
			"src/redact.ts:141:export function secretIds(secrets: readonly string[]): string[] {",
			"tests/violation.test.ts:281:  const secret: Violation = {",
			"src/output.ts:242:    secret: secretBlocks.length > 0,",
		}, "\n")},
	}
	for _, s := range samples {
		eqStrings(t, FindSecrets(s[1]), nil, s[0])
	}
	for _, form := range []string{"<redacted>", "<...>", "<sk-live-token>", "***", "************", "…", "...", "REDACTED", "ReDaCtEd", "xxxx", "XXXXXXXX"} {
		eqStrings(t, FindSecrets("API_KEY="+form), nil, form)
	}
	for _, text := range []string{"API_KEY=19415506", "API_KEY=797330_123", "Authorization: Bearer 19,415,506"} {
		eqStrings(t, FindSecrets(text), nil, text)
	}
	for _, text := range []string{
		"secretValues secretIds unseenSecrets findSecrets(text)",
		"const unseenSecrets = secretValues.filter((id) => !secretsSeen.has(id));",
		"const secrets = findSecrets(body);",
		"verdict.secretIds = secretIds(real);",
		"const secretId = output.secretIds;",
		`const secret: Violation = { id: "x" };`,
	} {
		eqStrings(t, FindSecrets(text), nil, text)
	}
	key := join("k7Rm2Qx9Lp4Ns8Vt1Wd6", "Yc3Zb5Hf7Jg0Pk2Mn5Qz")
	if len(key) != 40 {
		t.Fatal("fixture length")
	}
	eqStrings(t, FindSecrets("API_KEY="+key), []string{key}, "real key found once")
	if !LooksLikeSecretValue(key) {
		t.Fatal("key does not look like a secret value")
	}
	for _, text := range []string{"DB_PASSWORD=Tr0ub(4dor)&3xK9", "API_KEY=abc[123]DEFghi789", "password: s3cr3t{Qz}Lk8m"} {
		if len(FindSecrets(text)) < 1 {
			t.Fatalf("a bracket inside a password is a password: %q", text)
		}
	}
}

var (
	csAccessKey = join("AKIA3M7QZ2", "PRT9LVXW8Y")
	csSignature = join("9c4e1a7b2f8d3e6a0b5c9d2e7f1a4b8c", "3d6e0f2a5b9c1d4e7f8a2b6c0d3e5f91")
	csOpaque    = join("Qx7Lm2Rt9Vk4", "Np8Wd3Hz6Jc1")
)

func assertSignedURL(t *testing.T, name, text string) {
	t.Helper()
	found := FindSecrets(text)
	if len(found) == 0 {
		t.Fatalf("%s: the URL carries credential-shaped values", name)
	}
	real, _ := PartitionSecretsIn(found, text)
	eqStrings(t, real, nil, name+": no notice")
	if out, n := MaskSecrets(text); out != text || n != 0 {
		t.Fatalf("%s: no masking, got %q %d", name, out, n)
	}
}

// twin: tests/credential-shapes.test.ts:63
func TestAnS3PresignedURLIsNotACredential(t *testing.T) {
	assertSignedURL(t, "s3", `"url": "https://uploads.s3.us-east-1.amazonaws.com/team/a1/shot.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=`+csAccessKey+`%2F20260924%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20260924T101010Z&X-Amz-Expires=3600&X-Amz-SignedHeaders=host&X-Amz-Signature=`+csSignature+`"`)
}

// twin: tests/credential-shapes.test.ts:67
func TestAGCSSignedURLIsNotACredential(t *testing.T) {
	assertSignedURL(t, "gcs", `https://storage.googleapis.com/assets-7f3/doc.pdf?X-Goog-Algorithm=GOOG4-RSA-SHA256&X-Goog-Credential=uploader%40proj-4821.iam.gserviceaccount.com%2F20260924%2Fauto%2Fstorage%2Fgoog4_request&X-Goog-Date=20260924T101010Z&X-Goog-Expires=900&X-Goog-SignedHeaders=host&X-Goog-Signature=`+csSignature)
}

// twin: tests/credential-shapes.test.ts:71
func TestAStorageTokenURLWithAnExpiryIsNotACredential(t *testing.T) {
	assertSignedURL(t, "token", `![image](https://objects.storagehost.net/v1/files/report.pdf?se=2026-09-24T12%3A00%3A00Z&token=`+csOpaque+`)`)
}

// twin: tests/credential-shapes.test.ts:75
func TestABareTokenOutsideASignedURLStillCounts(t *testing.T) {
	bare := "token=" + csOpaque
	real, _ := PartitionSecretsIn(FindSecrets(bare), bare)
	eqStrings(t, real, []string{csOpaque}, "bare token")
	if out, n := MaskSecrets(bare); out != "token=[redacted]" || n != 1 {
		t.Fatalf("got %q %d", out, n)
	}
	unsigned := "https://objects.storagehost.net/v1/files/report.pdf?token=" + csOpaque
	real, _ = PartitionSecretsIn(FindSecrets(unsigned), unsigned)
	eqStrings(t, real, []string{csOpaque}, "no expiry: not a signed URL")
	reused := "https://objects.storagehost.net/f.pdf?Expires=1790000000&token=" + csOpaque + "\nAPI_TOKEN=" + csOpaque
	real, _ = PartitionSecretsIn(FindSecrets(reused), reused)
	eqStrings(t, real, []string{csOpaque}, "reused on its own")
	if strings.Contains(Redact("https://objects.storagehost.net/f.pdf?se=2026-09-24&token="+csOpaque), csOpaque) {
		t.Fatal("redaction of a signed URL must stay unchanged")
	}
}

// twin: tests/credential-shapes.test.ts:88
func TestACredentialValueContainingAmpersandIsRedactedWhole(t *testing.T) {
	password := join("Tr0ub4dor", "&3xK9")
	line := "DB_PASSWORD=" + password
	if got := Redact(line); got != "DB_PASSWORD=[redacted]" {
		t.Fatal(got)
	}
	eqStrings(t, FindSecrets(line), []string{password}, "find")
	if out, n := MaskSecrets(line); out != "DB_PASSWORD=[redacted]" || n != 1 {
		t.Fatalf("%q %d", out, n)
	}
	token := join("abc123", "def456")
	query := "https://x.test/cb?token=" + token + "&next=/home"
	if got := Redact(query); got != "https://x.test/cb?token=[redacted]&next=/home" {
		t.Fatal(got)
	}
	eqStrings(t, FindSecrets(query), []string{token}, "query")
	if out, _ := MaskSecrets(query); out != "https://x.test/cb?token=[redacted]&next=/home" {
		t.Fatal(out)
	}
	key := join("k7Rm2Qx9", "Lp4Ns8Vt")
	chained := "API_KEY=" + key + " && npm test"
	if got := Redact(chained); got != "API_KEY=[redacted] && npm test" {
		t.Fatal(got)
	}
	eqStrings(t, FindSecrets(chained), []string{key}, "chained")
	if out, _ := MaskSecrets(chained); out != "API_KEY=[redacted] && npm test" {
		t.Fatal(out)
	}
	tight := "API_KEY=" + key + "&&npm test"
	if got := Redact(tight); got != "API_KEY=[redacted]&&npm test" {
		t.Fatal(got)
	}
	if out, _ := MaskSecrets(tight); out != "API_KEY=[redacted]&&npm test" {
		t.Fatal(out)
	}
}

// twin: tests/credential-shapes.test.ts:111
func TestAnS3PresignedURLMadeWithTemporaryCredentialsIsNotACredential(t *testing.T) {
	sessionToken := join("IQoJb3JpZ2luX2VjEJr%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDx7Kp2Rm9", "Lq4Tz8Wn3Vb6Hd1Jc5Ys0GaKu2Re7Nt4Mx9Lp3Vz8Hq1Wd6Bj5Cf0Ys2Tg7Nk%3D")
	assertSignedURL(t, "s3 temporary", "https://uploads.s3.us-east-1.amazonaws.com/team/a1/shot.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential="+join("ASIA3M7QZ2", "PRT9LVXW8Y")+"%2F20260924%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20260924T101010Z&X-Amz-Expires=3600&X-Amz-Security-Token="+sessionToken+"&X-Amz-SignedHeaders=host&X-Amz-Signature="+csSignature)
}

// twin: tests/credential-shapes.test.ts:116
func TestEveryValueFindSecretsReportsIsMasked(t *testing.T) {
	password := join("Vq7mZ2rK", "9xLp4Tn8")
	header := join("Hd3Jc5Ys0Ga", "Ku2Re7Nt4Mx9")
	bearer := join("Rt9Vk4Np8W", "d3Hz6Jc1Qx")
	cases := []string{
		"redis://worker:" + password + "@cache.internal:6379",
		"Authorization: " + header,
		`curl -H "Authorization: Bearer ` + bearer + `"`,
		join("ghs_", "Qx7Lm2Rt9Vk4Np8Wd3Hz6Jc1"), join("AIza", "Sy7Lm2Rt9Vk4Np8Wd3Hz6Jc1Qx7Lm2R"), join("xoxs-", "7342-Qx7Lm2Rt9Vk"),
	}
	for _, text := range cases {
		found, _ := PartitionSecretsIn(FindSecrets(text), text)
		if len(found) == 0 {
			t.Fatalf("%s: detected", text)
		}
		masked, n := MaskSecrets(text)
		if n < 1 {
			t.Fatalf("%s: masked", text)
		}
		for _, v := range found {
			if strings.Contains(masked, v) {
				t.Fatalf("%s: a detected value survives", text)
			}
		}
		left, _ := PartitionSecretsIn(FindSecrets(masked), masked)
		eqStrings(t, left, nil, text+": nothing left to announce")
	}
	if out, _ := MaskSecrets(cases[0]); out != "redis://worker:[redacted]@cache.internal:6379" {
		t.Fatal(out)
	}
}

// twin: tests/guard.test.ts:129
func TestFindSecretsNeedsAValueShape(t *testing.T) {
	talk := []string{
		"secret: boolean;", "const savedKey = process.env.TYPESAFE_API_KEY;", "TOKEN=${GITHUB_TOKEN}", "password: <your password>", "api_key: string", "token=$TOKEN",
		"export TYPESAFE_API_KEY", "resolveApiKey(): key from TYPESAFE_API_KEY", "password = 'changeme'", "Authorization: Bearer <token>", "credentials: undefined",
		"secret_key=[redacted]", "client_secret: os.environ['CLIENT_SECRET']", "token: synthetic-secret", `grep -n 'secret\|SECRET\|credential' src/output.ts`, "passwordField = true",
	}
	for _, text := range talk {
		eqStrings(t, FindSecrets(text), nil, text)
	}
	real := []string{
		"TOKEN=sk-synthetic-0123456789abcdef", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dGVzdHNpZ25hdHVyZTEyMw", "https://user:Pa55w0rd-x@example.com/db",
		"AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP", "password: hunter2hunter2X9", "api_key = 'a1b2c3d4e5f6g7h8'", "ghp_0123456789abcdefghijklmnopqrstuvwxyz", "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----",
	}
	for _, text := range real {
		if len(FindSecrets(text)) < 1 {
			t.Fatalf("not found: %q", text)
		}
	}
	eqStrings(t, FindSecrets("TOKEN=sk-synthetic-0123456789abcdef and again TOKEN=sk-synthetic-0123456789abcdef"), []string{"sk-synthetic-0123456789abcdef"}, "deduplicated")
	if SecretFingerprint([]string{"b", "a"}) != SecretFingerprint([]string{"a", "b"}) {
		t.Fatal("order matters")
	}
	if len(SecretFingerprint([]string{"a"})) != 12 {
		t.Fatal("fingerprint length")
	}
	idsA := SecretIDs([]string{"sk-synthetic-0123456789abcdef"})
	idsBoth := SecretIDs([]string{"sk-synthetic-0123456789abcdef", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"})
	if len(idsBoth) != 2 {
		t.Fatal("two ids")
	}
	shared := false
	for _, id := range idsBoth {
		shared = shared || id == idsA[0]
	}
	if !shared {
		t.Fatal("the shared value keeps its id when another value appears")
	}
	if SecretFingerprint([]string{"sk-synthetic-0123456789abcdef"}) == SecretFingerprint([]string{"sk-synthetic-0123456789abcdef", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"}) {
		t.Fatal("the set fingerprint must change")
	}
	if !(LooksLikeSecretValue("a1b2c3d4e5") && !LooksLikeSecretValue("abcdefgh") && !LooksLikeSecretValue("12345678") && !LooksLikeSecretValue("SOME_ENV_NAME") && !LooksLikeSecretValue("someCamelCase")) {
		t.Fatal("looksLikeSecretValue")
	}
	if got := Redact("secret: boolean;"); got != "secret: [redacted];" {
		t.Fatal(got)
	}
}

// twin: tests/guard.test.ts:155
func TestSyntheticishSeparatesFixtureStandInsFromKeys(t *testing.T) {
	standIns := []string{
		"devtok_9f8e7d6c5b4a3210", "sk-synthetic-0123456789abcdef", "sk-live-abcdefghij123456", "0123456789abcdef",
		"AKIAIOSFODNN7EXAMPLE", "example-api-key-1234", "test-token-abcdef123456", "placeholder-value-42", "changeme123",
		"hunter2hunter2X9", "deadbeefdeadbeef", "sampleSample1234", "abcabcabcabc", "aaaaaaaaaaaa", "fake_client_secret_1",
	}
	for _, v := range standIns {
		if !Syntheticish(v) {
			t.Fatalf("stand-in not recognised: %s", v)
		}
	}
	real := []string{"9f8e7d6c5b4a3210e1f2a3b4c5d6e7f8", "ghp_Qk7mZ2pR9vT4xL8nW3sY6bD1cF5hJ0aM", "AKIA3M7QZ2PRT9LVXW8Y", "Pa55w0rdX9", "a1b2c3d4e5f6g7h8", "api_key_9f8e7d6c5b4a3210", "7f3c9d2b8e1a4c6f2b9d", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dGVzdHNpZ25hdHVyZTEyMw"}
	for _, v := range real {
		if Syntheticish(v) {
			t.Fatalf("real shape demoted: %s", v)
		}
	}
	found := FindSecrets("DEV_TOKEN=devtok_9f8e7d6c5b4a3210\nSUPABASE_ACCESS_TOKEN=9f8e7d6c5b4a3210e1f2a3b4c5d6e7f8")
	if len(found) != 2 {
		t.Fatalf("found %q", found)
	}
	keys, synthetic := PartitionSecrets(found)
	eqStrings(t, keys, []string{"9f8e7d6c5b4a3210e1f2a3b4c5d6e7f8"}, "real")
	eqStrings(t, synthetic, []string{"devtok_9f8e7d6c5b4a3210"}, "synthetic")
}
