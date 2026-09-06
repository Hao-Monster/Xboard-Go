package httpapi

import (
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

// distributorOrderResponse is the public projection of a distributor order.
// Keep this DTO explicit: store.DistributorOrder also carries the subscription
// token, subscriber UUID and one-time claim token required by internal flows.
type distributorOrderResponse struct {
	Order                 distributorFinancialOrderResponse `json:"order"`
	PlanName              string                            `json:"plan_name"`
	DistributorEmail      string                            `json:"distributor_email,omitempty"`
	DistributorName       string                            `json:"distributor_name,omitempty"`
	Subscription          distributorSubscriptionResponse   `json:"subscription"`
	SettlementStatus      store.DistributorSettlementStatus `json:"settlement_status"`
	Entitlement           distributorEntitlementResponse    `json:"subscription_entitlement"`
	BoundDevices          []string                          `json:"bound_devices"`
	IsSubscriptionOrigin  bool                              `json:"is_subscription_origin"`
	CanViewSubscriptionQR bool                              `json:"can_view_subscription_qr"`
	CanRenew              bool                              `json:"can_renew"`
}

type distributorFinancialOrderResponse struct {
	ID                         int64             `json:"id"`
	UserID                     int64             `json:"user_id"`
	PlanID                     int64             `json:"plan_id"`
	PaymentID                  *int64            `json:"payment_id"`
	Period                     string            `json:"period"`
	TradeNo                    string            `json:"trade_no"`
	OriginalAmount             int64             `json:"original_amount"`
	TotalAmount                int64             `json:"total_amount"`
	HandlingAmount             *int64            `json:"handling_amount"`
	BalanceAmount              int64             `json:"balance_amount"`
	SurplusCredit              int64             `json:"surplus_credit"`
	SurplusAmount              int64             `json:"surplus_amount"`
	Type                       store.OrderType   `json:"type"`
	Status                     store.OrderStatus `json:"status"`
	SurplusOrderIDs            []int64           `json:"surplus_order_ids"`
	CouponID                   *int64            `json:"coupon_id"`
	CommissionStatus           *int              `json:"commission_status"`
	InviteUserID               *int64            `json:"invite_user_id"`
	ActualCommissionBalance    *int64            `json:"actual_commission_balance"`
	CommissionRate             *int              `json:"commission_rate"`
	CommissionAutoCheck        *bool             `json:"commission_auto_check"`
	CommissionBalance          int64             `json:"commission_balance"`
	DiscountAmount             int64             `json:"discount_amount"`
	PaidAt                     *time.Time        `json:"paid_at"`
	CallbackNo                 string            `json:"callback_no"`
	EntitlementExpiredAtBefore *time.Time        `json:"entitlement_expired_at_before"`
	EntitlementExpiredAtAfter  *time.Time        `json:"entitlement_expired_at_after"`
	CreatedAt                  time.Time         `json:"created_at"`
	UpdatedAt                  time.Time         `json:"updated_at"`
}

type distributorSubscriptionResponse struct {
	ID                int64                             `json:"id"`
	OriginalOrderID   int64                             `json:"original_order_id"`
	OriginalTradeNo   string                            `json:"trade_no"`
	DistributorUserID int64                             `json:"distributor_user_id"`
	CustomerName      *string                           `json:"customer_name"`
	Remark            *string                           `json:"remark"`
	DeliveryStatus    store.DistributorDeliveryStatus   `json:"delivery_status"`
	SettlementStatus  store.DistributorSettlementStatus `json:"settlement_status"`
	ConfigIssuedAt    *time.Time                        `json:"config_issued_at"`
	ConnectedAt       *time.Time                        `json:"connected_at"`
	ConnectedNodeID   *int64                            `json:"connected_node_id"`
	ConnectedNodeName *string                           `json:"connected_node_name"`
	ClaimedAt         *time.Time                        `json:"claimed_at"`
	ClosedAt          *time.Time                        `json:"closed_at"`
	HWIDEnabled       bool                              `json:"hwid_enabled"`
	HWIDLimit         int                               `json:"hwid_limit"`
	Revision          int64                             `json:"revision"`
	CreatedAt         time.Time                         `json:"created_at"`
	UpdatedAt         time.Time                         `json:"updated_at"`
}

type distributorEntitlementResponse struct {
	PlanID           int64      `json:"plan_id"`
	PlanName         string     `json:"plan_name"`
	TransferEnable   int64      `json:"transfer_enable"`
	UsedTraffic      int64      `json:"used_traffic"`
	RemainingTraffic int64      `json:"remaining_traffic"`
	ExpiredAt        *time.Time `json:"expired_at"`
	SpeedLimit       int        `json:"speed_limit"`
	DeviceLimit      int        `json:"device_limit"`
}

type distributorOrderPageResponse struct {
	Items    []distributorOrderResponse `json:"items"`
	Total    int64                      `json:"total"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"page_size"`
}

func distributorOrderResponseOf(value store.DistributorOrder) distributorOrderResponse {
	return distributorOrderResponse{
		Order: distributorFinancialOrderResponse{
			ID: value.Order.ID, UserID: value.Order.UserID, PlanID: value.Order.PlanID, PaymentID: value.Order.PaymentID,
			Period: value.Order.Period, TradeNo: value.Order.TradeNo, OriginalAmount: value.Order.OriginalAmount,
			TotalAmount: value.Order.TotalAmount, HandlingAmount: value.Order.HandlingAmount, BalanceAmount: value.Order.BalanceAmount,
			SurplusCredit: value.Order.SurplusCredit, SurplusAmount: value.Order.SurplusAmount, Type: value.Order.Type,
			Status: value.Order.Status, SurplusOrderIDs: value.Order.SurplusOrderIDs, CouponID: value.Order.CouponID,
			CommissionStatus: value.Order.CommissionStatus, InviteUserID: value.Order.InviteUserID,
			ActualCommissionBalance: value.Order.ActualCommissionBalance, CommissionRate: value.Order.CommissionRate,
			CommissionAutoCheck: value.Order.CommissionAutoCheck, CommissionBalance: value.Order.CommissionBalance,
			DiscountAmount: value.Order.DiscountAmount, PaidAt: value.Order.PaidAt, CallbackNo: value.Order.CallbackNo,
			EntitlementExpiredAtBefore: value.Order.EntitlementExpiredAtBefore,
			EntitlementExpiredAtAfter:  value.Order.EntitlementExpiredAtAfter,
			CreatedAt:                  value.Order.CreatedAt, UpdatedAt: value.Order.UpdatedAt,
		},
		PlanName: value.PlanName, DistributorEmail: value.DistributorEmail, DistributorName: value.DistributorName,
		Subscription: distributorSubscriptionResponse{
			ID: value.Subscription.ID, OriginalOrderID: value.Subscription.OriginalOrderID,
			OriginalTradeNo: value.Subscription.OriginalTradeNo, DistributorUserID: value.Subscription.DistributorUserID,
			CustomerName: value.Subscription.CustomerName, Remark: value.Subscription.Remark,
			DeliveryStatus: value.Subscription.DeliveryStatus, SettlementStatus: value.Subscription.SettlementStatus,
			ConfigIssuedAt: value.Subscription.ConfigIssuedAt, ConnectedAt: value.Subscription.ConnectedAt,
			ConnectedNodeID: value.Subscription.ConnectedNodeID, ConnectedNodeName: value.Subscription.ConnectedNodeName,
			ClaimedAt: value.Subscription.ClaimedAt, ClosedAt: value.Subscription.ClosedAt,
			HWIDEnabled: value.Subscription.HWIDEnabled, HWIDLimit: value.Subscription.HWIDLimit,
			Revision: value.Subscription.Revision, CreatedAt: value.Subscription.CreatedAt, UpdatedAt: value.Subscription.UpdatedAt,
		},
		SettlementStatus: value.SettlementStatus,
		Entitlement: distributorEntitlementResponse{
			PlanID: value.Entitlement.PlanID, PlanName: value.Entitlement.PlanName,
			TransferEnable: value.Entitlement.TransferEnable, UsedTraffic: value.Entitlement.UsedTraffic,
			RemainingTraffic: value.Entitlement.RemainingTraffic, ExpiredAt: value.Entitlement.ExpiredAt,
			SpeedLimit: value.Entitlement.SpeedLimit, DeviceLimit: value.Entitlement.DeviceLimit,
		},
		BoundDevices: value.BoundDevices, IsSubscriptionOrigin: value.IsSubscriptionOrigin,
		CanViewSubscriptionQR: value.CanViewSubscriptionQR, CanRenew: value.CanRenew,
	}
}

func distributorOrderPageResponseOf(value store.DistributorOrderPage) distributorOrderPageResponse {
	items := make([]distributorOrderResponse, len(value.Items))
	for index := range value.Items {
		items[index] = distributorOrderResponseOf(value.Items[index])
	}
	return distributorOrderPageResponse{Items: items, Total: value.Total, Page: value.Page, PageSize: value.PageSize}
}
