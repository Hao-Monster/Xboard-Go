package legacymigration

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestReadHumanUsersPreservesImportedDigestAndHistory(t *testing.T) {
	path := createLegacyHumanUsersSnapshot(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("fixture-passwordfixture-salt"))
	_, err = db.Exec(`UPDATE v2_user SET password_algo='sha256salt',password_salt='fixture-salt',password=?,t=1700000001,last_login_ip='2001:db8::1',online_count=3 WHERE id=2`, fmt.Sprintf("%x", sum))
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadHumanUsersSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	user := snapshot.Users[1]
	if user.History.LegacyTime != 1700000001 || user.History.LastLoginIP != "2001:db8::1" || user.History.OnlineCount != 3 || !security.DefaultPasswordHasher().Verify("fixture-password", user.PasswordHash) {
		t.Fatal("import lost supported credentials or history")
	}
}

func TestReadPlansPreservesAudienceAndRejectsWrongRole(t *testing.T) {
	path := createLegacyPlansSnapshot(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`ALTER TABLE v2_plan ADD COLUMN customer_visibility TEXT DEFAULT 'all';
 ALTER TABLE v2_plan ADD COLUMN distributor_visibility TEXT DEFAULT 'all';
 CREATE TABLE v2_plan_visibility_user(plan_id INTEGER,user_id INTEGER,audience TEXT);
 CREATE TABLE v2_user(id INTEGER PRIMARY KEY,is_distributor INTEGER);
 INSERT INTO v2_user VALUES(5,0),(6,1);
 INSERT INTO v2_plan(id,transfer_enable,name,show,renew,sell,created_at,updated_at,customer_visibility,distributor_visibility) VALUES(1,10,'Restricted',1,1,1,1700000000,1700000000,'selected','selected');
 INSERT INTO v2_plan_visibility_user VALUES(1,5,'customer'),(1,6,'distributor');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadPlansSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	p := snapshot.Plans[0]
	if p.CustomerVisibility != "selected" || p.DistributorVisibility != "selected" || len(p.CustomerUserIDs) != 1 || p.CustomerUserIDs[0] != 5 || len(p.DistributorUserIDs) != 1 || p.DistributorUserIDs[0] != 6 {
		t.Fatal("audience information was not preserved")
	}
	db, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE v2_plan_visibility_user SET user_id=6 WHERE audience='customer'`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadPlansSnapshot(t.Context(), path); err == nil {
		t.Fatal("wrong-role membership accepted")
	}
}
