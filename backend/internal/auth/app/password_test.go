package app

import "testing"

func TestPasswordHash(t *testing.T) {
	a, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Hash("correct horse battery staple")
	if a == b || !Verify(a, "correct horse battery staple") || Verify(a, "incorrect password") {
		t.Fatal("password verification or unique salt failed")
	}
	for _, bad := range []string{"plaintext", "$argon2id$v=19$m=999999999,t=3,p=1$bad$bad", prefix + "bad$bad"} {
		if Verify(bad, "password") {
			t.Fatal("accepted invalid hash")
		}
	}
	if _, err := Hash("short"); err == nil {
		t.Fatal("accepted short password")
	}
}
