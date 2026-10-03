package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrGiftPurchaseConversion = errors.New("purchase code entitlements cannot be converted to panel credit")
	ErrGiftPurchasePlan       = errors.New("purchase code plan does not match current subscription")
	ErrGiftPurchaseMode       = errors.New("purchase code mode does not match current subscription")
	ErrGiftPurchasePending    = errors.New("resolve pending orders before redeeming purchase code")
)

type GiftPurchaseSnapshot struct {
	PlanID         int64  `json:"plan_id"`
	PlanName       string `json:"plan_name"`
	Period         string `json:"period"`
	TransferEnable int64  `json:"transfer_enable"`
	GroupID        *int64 `json:"group_id"`
	SpeedLimit     int    `json:"speed_limit"`
	DeviceLimit    int    `json:"device_limit"`
	ResetMethod    int    `json:"reset_method"`
}

type GiftPurchasePreview struct {
	TransferBefore int64      `json:"transfer_before"`
	TransferAfter  int64      `json:"transfer_after"`
	UsedTraffic    int64      `json:"used_traffic"`
	ExpiresBefore  *time.Time `json:"expires_before"`
	ExpiresAfter   *time.Time `json:"expires_after"`
	Renewal        bool       `json:"renewal"`
}

// Alter only the constrained column so existing parent/child foreign keys survive.
const schemaV68GiftPurchase = `
DROP INDEX idx_gift_templates_active;
ALTER TABLE gift_card_templates RENAME COLUMN type TO legacy_type;
ALTER TABLE gift_card_templates ADD COLUMN type INTEGER NOT NULL DEFAULT 1 CHECK(type BETWEEN 1 AND 4);
UPDATE gift_card_templates SET type = legacy_type;
ALTER TABLE gift_card_templates DROP COLUMN legacy_type;
CREATE INDEX idx_gift_templates_active ON gift_card_templates(status, type, sort_position, id);
ALTER TABLE orders ADD COLUMN source TEXT NOT NULL DEFAULT 'online' CHECK(source IN ('online','gift_card_purchase'));
ALTER TABLE orders ADD COLUMN gift_card_code_id INTEGER REFERENCES gift_card_codes(id) ON DELETE RESTRICT;
ALTER TABLE orders ADD COLUMN purchase_snapshot_json TEXT CHECK(purchase_snapshot_json IS NULL OR (json_valid(purchase_snapshot_json) AND json_type(purchase_snapshot_json) = 'object'));
` + schemaV68GiftPurchaseObjects

const schemaV68GiftPurchaseObjects = `
CREATE UNIQUE INDEX idx_orders_gift_purchase ON orders(gift_card_code_id) WHERE gift_card_code_id IS NOT NULL;
CREATE TRIGGER gift_purchase_order_guard BEFORE INSERT ON orders WHEN NEW.source = 'gift_card_purchase'
AND (NEW.gift_card_code_id IS NULL OR NEW.purchase_snapshot_json IS NULL OR NEW.status <> 3 OR NEW.total_amount <> 0 OR NEW.original_amount <> 0 OR NEW.balance_amount <> 0 OR NEW.commission_balance <> 0 OR NEW.payment_id IS NOT NULL OR NEW.paid_at IS NOT NULL)
BEGIN SELECT RAISE(ABORT, 'invalid gift purchase order'); END;
CREATE TRIGGER gift_template_type_guard BEFORE UPDATE OF type ON gift_card_templates
WHEN NEW.type <> OLD.type AND (NEW.type = 4 OR OLD.type = 4) AND EXISTS(SELECT 1 FROM gift_card_codes WHERE template_id = OLD.id)
BEGIN SELECT RAISE(ABORT, 'issued template type is immutable'); END;
CREATE TRIGGER gift_purchase_snapshot_guard BEFORE UPDATE OF metadata_json, max_usage, code ON gift_card_codes
WHEN json_extract(OLD.metadata_json, '$.purchase_snapshot') IS NOT NULL AND (NEW.metadata_json <> OLD.metadata_json OR NEW.max_usage <> 1 OR NEW.code <> OLD.code)
BEGIN SELECT RAISE(ABORT, 'purchase code rights are immutable'); END;
`

func purchaseSnapshot(ctx context.Context, tx *sql.Tx, reward GiftCardReward, now time.Time) (*GiftPurchaseSnapshot, error) {
	plan, err := getPlan(ctx, tx, *reward.PlanID, now)
	if err != nil {
		return nil, err
	}
	if _, ok := plan.Prices[reward.PurchasePeriod]; !ok {
		return nil, ErrInvalidInput
	}
	if plan.TransferEnableGiB <= 0 || plan.TransferEnableGiB > maxGiftCardTransfer/bytesPerGiB {
		return nil, ErrInvalidInput
	}
	reset, err := readSystemTrafficResetMethod(ctx, tx)
	if err != nil {
		return nil, err
	}
	if plan.ResetTrafficMethod != nil {
		reset = *plan.ResetTrafficMethod
	}
	return &GiftPurchaseSnapshot{PlanID: plan.ID, PlanName: plan.Name, Period: reward.PurchasePeriod,
		TransferEnable: plan.TransferEnableGiB * bytesPerGiB, GroupID: plan.GroupID,
		SpeedLimit: optionalPlanInt(plan.SpeedLimit), DeviceLimit: optionalPlanInt(plan.DeviceLimit), ResetMethod: reset}, nil
}

func giftPurchasePreview(user giftCardUserState, snapshot *GiftPurchaseSnapshot, now time.Time) (*GiftPurchasePreview, error) {
	if snapshot == nil || snapshot.PlanID < 1 || snapshot.TransferEnable <= 0 || snapshot.TransferEnable > maxGiftCardTransfer {
		return nil, ErrInvalidInput
	}
	if snapshot.Period != "onetime" && orderPeriodMonths[snapshot.Period] == 0 {
		return nil, ErrInvalidInput
	}
	renewal := user.planID.Valid
	if renewal && user.planID.Int64 != snapshot.PlanID {
		return nil, ErrGiftPurchasePlan
	}
	if renewal && (user.expiredAt.Valid == (snapshot.Period == "onetime")) {
		return nil, ErrGiftPurchaseMode
	}
	p := &GiftPurchasePreview{TransferBefore: user.transferEnable, TransferAfter: snapshot.TransferEnable,
		UsedTraffic: user.trafficUpload + user.trafficDownload, ExpiresBefore: nullableUnixTime(user.expiredAt), Renewal: renewal}
	if !renewal {
		p.UsedTraffic = 0
	}
	if snapshot.Period == "onetime" {
		if renewal {
			if user.transferEnable > maxGiftCardTransfer-snapshot.TransferEnable {
				return nil, ErrInvalidInput
			}
			p.TransferAfter += user.transferEnable
		}
	} else {
		base := now
		if renewal && user.expiredAt.Valid && user.expiredAt.Int64 > now.Unix() {
			base = time.Unix(user.expiredAt.Int64, 0)
		}
		expires := addOrderMonths(base, orderPeriodMonths[snapshot.Period])
		if expires.Year() > 9999 {
			return nil, ErrInvalidInput
		}
		p.ExpiresAfter = &expires
		// A renewal extends time only; it must not replace previously granted quota.
		if renewal {
			p.TransferAfter = user.transferEnable
		}
	}
	return p, nil
}

func applyGiftPurchase(ctx context.Context, tx *sql.Tx, user *giftCardUserState, preview GiftCardPreview, now time.Time) (string, error) {
	snapshot := preview.Rewards.PurchaseSnapshot
	p, err := giftPurchasePreview(*user, snapshot, now)
	if err != nil {
		return "", err
	}
	before, err := marshalEntitlementSnapshot(user.orderUserState)
	if err != nil {
		return "", err
	}
	typeOfOrder := OrderTypeNew
	if p.Renewal {
		typeOfOrder = OrderTypeRenewal
	}
	user.planID = sql.NullInt64{Int64: snapshot.PlanID, Valid: true}
	user.transferEnable = p.TransferAfter
	user.expiredAt = sql.NullInt64{}
	if p.ExpiresAfter != nil {
		user.expiredAt = sql.NullInt64{Int64: p.ExpiresAfter.Unix(), Valid: true}
	}
	if !p.Renewal {
		user.trafficUpload, user.trafficDownload = 0, 0
		user.groupID = pointerToNullInt64(snapshot.GroupID)
		user.speedLimit, user.deviceLimit = snapshot.SpeedLimit, snapshot.DeviceLimit
		user.nextResetAt = nullableTime(CalculateNextTrafficReset(&snapshot.ResetMethod, snapshot.ResetMethod, p.ExpiresAfter, now))
	}
	if snapshot.Period != "onetime" && !user.nextResetAt.Valid {
		user.nextResetAt = nullableTime(CalculateNextTrafficReset(&snapshot.ResetMethod, snapshot.ResetMethod, p.ExpiresAfter, now))
	}
	if snapshot.Period == "onetime" {
		user.nextResetAt = sql.NullInt64{}
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET plan_id=?,group_id=?,transfer_enable=?,traffic_u=?,traffic_d=?,expired_at=?,speed_limit=?,device_limit=?,next_reset_at=?,admin_revision=admin_revision+1,updated_at=? WHERE id=?`,
		snapshot.PlanID, nullableSQLInt(user.groupID), user.transferEnable, user.trafficUpload, user.trafficDownload, nullableSQLInt(user.expiredAt), user.speedLimit, user.deviceLimit, nullableSQLInt(user.nextResetAt), now.Unix(), user.id)
	if err != nil {
		return "", fmt.Errorf("apply purchase entitlement: %w", err)
	}
	trade, err := newOrderTradeNo(now)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO orders(user_id,plan_id,period,trade_no,original_amount,total_amount,type,status,commission_status,source,gift_card_code_id,purchase_snapshot_json,entitlement_expired_at_before,entitlement_expired_at_after,created_at,updated_at) VALUES(?,?,?,?,0,0,?,3,3,'gift_card_purchase',?,?,?,?,?,?)`,
		user.id, snapshot.PlanID, snapshot.Period, trade, typeOfOrder, preview.Code.ID, string(encoded), nullableTimeUnix(p.ExpiresBefore), nullableTimeUnix(p.ExpiresAfter), now.Unix(), now.Unix())
	if err != nil {
		return "", fmt.Errorf("record purchase order: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return "", err
	}
	after, err := marshalEntitlementSnapshot(user.orderUserState)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO order_entitlement_events(order_id,user_id,event_type,before_json,after_json,applied_at) VALUES(?,?,?,?,?,?)`, id, user.id, orderTypeEventName(typeOfOrder), string(before), string(after), now.Unix())
	return trade, err
}

// Some existing migration repair paths retain later additive schema objects.
// Validate them rather than replaying destructive column operations.
func migrateGiftPurchase(ctx context.Context, tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name='source'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 1 {
		var definition string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name='gift_card_templates'`).Scan(&definition); err != nil {
			return err
		}
		if strings.Contains(normalizeProtectedSchemaDefinition(definition), "check(typebetween1and3)") {
			migration := strings.Split(schemaV68GiftPurchase, "ALTER TABLE orders")[0]
			if _, err := tx.ExecContext(ctx, migration); err != nil {
				return err
			}
		}
		declarations := strings.ReplaceAll(schemaV68GiftPurchaseObjects, "CREATE TRIGGER ", "CREATE TRIGGER IF NOT EXISTS ")
		declarations = strings.ReplaceAll(declarations, "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX IF NOT EXISTS ")
		if _, err := tx.ExecContext(ctx, declarations); err != nil {
			return err
		}
		return validateDeclaredSchemaObjects(ctx, tx, schemaV68GiftPurchaseObjects)
	}
	_, err := tx.ExecContext(ctx, schemaV68GiftPurchase)
	return err
}
