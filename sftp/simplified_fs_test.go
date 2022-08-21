package sftp

import "testing"

func TestContainsValidDir(t *testing.T) {
	if !ContainsValidDir("/asd/nasd/asd/") {
		t.Errorf("1 failed")
		t.Fail()
	}
	if ContainsValidDir("/asd/na/../sd/asd/") {
		t.Errorf("2 failed")
		t.Fail()
	}
	if ContainsValidDir("../asd/na/sd/asd/") {
		t.Errorf("3 failed")
		t.Fail()
	}
	if ContainsValidDir("/../asd/na/sd/asd/") {
		t.Errorf("4 failed")
		t.Fail()
	}
	if ContainsValidDir("/aa/dd/../../asd/na/sd/asd/") {
		t.Errorf("5 failed")
		t.Fail()
	}
	if ContainsValidDir("/aa/dd/./asd/na/sd/asd/") {
		t.Errorf("6 failed")
		t.Fail()
	}
	if ContainsValidDir("./aa/dd/asd/na/sd/asd/") {
		t.Errorf("7 failed")
		t.Fail()
	}
	if ContainsValidDir("//aa/dd/asd/na/sd/asd/") {
		t.Errorf("8 failed")
		t.Fail()
	}
	if ContainsValidDir("/aa/dd//asd/na/sd/asd/") {
		t.Errorf("9 failed")
		t.Fail()
	}
	if !ContainsValidDir("/aa/.../dd/asd/na/sd/asd/") {
		t.Errorf("10 failed")
		t.Fail()
	}
	if !ContainsValidDir("/aa/.sa/dd/asd/na/sd/asd/") {
		t.Errorf("11 failed")
		t.Fail()
	}
	if !ContainsValidDir("/aa/..sa/dd/asd/na/sd../asd/") {
		t.Errorf("12 failed")
		t.Fail()
	}
	if ContainsValidDir("/aa/..sa/dd/asd/na/sd../asd/../") {
		t.Errorf("13 failed")
		t.Fail()
	}
	if ContainsValidDir("/aa/..s/dd/asda/sd../asd/..") {
		t.Errorf("14 failed")
		t.Fail()
	}
	if ContainsValidDir("/aa/..s/dd/asda/sd../asd/.") {
		t.Errorf("15 failed")
		t.Fail()
	}
}
