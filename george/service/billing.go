package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"gorm.io/gorm"
)

// PlanOf devolve o plano da org (free quando não há assinatura).
func (s *Svc) PlanOf(orgID uint) (*gm.Plan, error) {
	p := &gm.Plan{}
	err := s.DB.Raw(`SELECT p.* FROM plans p JOIN subscriptions s ON s.plan_id = p.id WHERE s.org_id = ?`, orgID).Scan(p).Error
	if err != nil {
		return nil, err
	}
	if p.ID == 0 {
		if err := s.DB.Where("code = ?", "free").First(p).Error; err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Limits calcula limites do plano + uso atual.
func (s *Svc) Limits(orgID uint) (*perm.Limits, error) {
	plan, err := s.PlanOf(orgID)
	if err != nil {
		return nil, err
	}
	lim := &perm.Limits{MaxDevices: plan.MaxDevices, MaxTechs: plan.MaxTechs}
	var n int64
	s.DB.Model(&gm.Device{}).Where("org_id = ?", orgID).Count(&n)
	lim.Devices = int(n)
	s.DB.Model(&User{}).Where("org_id = ? AND status = 1 AND role = 'tech'", orgID).Count(&n)
	lim.Techs = int(n)
	return lim, nil
}

// BillingView é GET /org/billing.
type BillingView struct {
	Plan         *gm.Plan         `json:"plan"`
	Subscription *gm.Subscription `json:"subscription"`
	Usage        *perm.Limits     `json:"usage"`
	StripeReady  bool             `json:"stripe_configured"`
	HasCustomer  bool             `json:"has_stripe_customer"`
}

// Billing resume plano/uso.
func (s *Svc) Billing(c Ctx, orgID uint) (*BillingView, error) {
	if !perm.Can(c.Actor(), perm.OrgBilling, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	plan, err := s.PlanOf(orgID)
	if err != nil {
		return nil, err
	}
	lim, err := s.Limits(orgID)
	if err != nil {
		return nil, err
	}
	sub := &gm.Subscription{}
	if err := s.DB.Where("org_id = ?", orgID).First(sub).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &BillingView{Plan: plan, Subscription: sub, Usage: lim, StripeReady: s.Cfg.StripeSecretKey != "", HasCustomer: sub.StripeCustomerID != ""}, nil
}

// BillingPortal cria uma sessão do portal Stripe (precisa de chave + customer).
func (s *Svc) BillingPortal(c Ctx, orgID uint, returnURL string) (string, error) {
	if !perm.Can(c.Actor(), perm.OrgBilling, perm.Resource{OrgID: orgID}) {
		return "", ErrForbidden()
	}
	if s.Cfg.StripeSecretKey == "" {
		return "", ErrNotConfigured("billing")
	}
	sub := &gm.Subscription{}
	if err := s.DB.Where("org_id = ?", orgID).First(sub).Error; err != nil || sub.StripeCustomerID == "" {
		return "", ErrConflict("no_stripe_customer", "org ainda não possui cliente Stripe")
	}
	if returnURL == "" {
		returnURL = s.Cfg.PublicURL + "/app/settings/billing"
	}
	form := url.Values{"customer": {sub.StripeCustomerID}, "return_url": {returnURL}}
	req, _ := http.NewRequest(http.MethodPost, "https://api.stripe.com/v1/billing_portal/sessions", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+s.Cfg.StripeSecretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("portal-%d-%d", orgID, time.Now().Unix()/30))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", &Error{Status: http.StatusBadGateway, Code: "stripe_error", Message: "Stripe recusou a criação do portal", Details: string(body)}
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	s.Audit(c, orgID, "billing.portal", "org", orgID, nil)
	return out.URL, nil
}

// VerifyStripeSignature valida o header Stripe-Signature (HMAC-SHA256, tolerância 5 min).
func VerifyStripeSignature(secret string, payload []byte, header string, now time.Time) error {
	if secret == "" {
		return errors.New("webhook secret não configurado")
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts == "" || len(sigs) == 0 {
		return errors.New("assinatura ausente")
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("timestamp inválido")
	}
	if d := now.Unix() - t; d > 300 || d < -300 {
		return errors.New("assinatura fora da janela de tolerância")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, sg := range sigs {
		if hmac.Equal([]byte(sg), []byte(want)) {
			return nil
		}
	}
	return errors.New("assinatura inválida")
}

// StripeWebhook processa o evento (idempotente por id).
func (s *Svc) StripeWebhook(payload []byte, header string) (status string, err error) {
	if err := VerifyStripeSignature(s.Cfg.StripeWebhookSecret, payload, header, time.Now()); err != nil {
		return "", ErrUnauthorized("invalid_signature", err.Error())
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object map[string]interface{} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil || ev.ID == "" {
		return "", ErrValidation("evento inválido", nil)
	}
	res := s.DB.Exec(`INSERT INTO stripe_events (id, type, received_at) VALUES (?, ?, now()) ON CONFLICT (id) DO NOTHING`, ev.ID, ev.Type)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "duplicate", nil
	}
	obj := ev.Data.Object
	str := func(k string) string {
		v, _ := obj[k].(string)
		return v
	}
	switch ev.Type {
	case "checkout.session.completed":
		// client_reference_id = org id; liga o customer à org
		if ref := str("client_reference_id"); ref != "" {
			if orgID, e := strconv.Atoi(ref); e == nil {
				s.DB.Exec(`UPDATE subscriptions SET stripe_customer_id = ?, stripe_subscription_id = ?, updated_at = now() WHERE org_id = ?`,
					str("customer"), str("subscription"), orgID)
			}
		}
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		customer := str("customer")
		stStr := str("status")
		if ev.Type == "customer.subscription.deleted" {
			stStr = "canceled"
		}
		var planID uint
		if items, ok := obj["items"].(map[string]interface{}); ok {
			if data, ok := items["data"].([]interface{}); ok && len(data) > 0 {
				if first, ok := data[0].(map[string]interface{}); ok {
					if price, ok := first["price"].(map[string]interface{}); ok {
						if pid, _ := price["id"].(string); pid != "" {
							s.DB.Raw(`SELECT id FROM plans WHERE stripe_price_id = ?`, pid).Scan(&planID)
						}
					}
				}
			}
		}
		if stStr == "canceled" {
			s.DB.Raw(`SELECT id FROM plans WHERE code = 'free'`).Scan(&planID)
		}
		upd := map[string]interface{}{"status": stStr, "stripe_subscription_id": str("id"), "updated_at": time.Now()}
		if planID != 0 {
			upd["plan_id"] = planID
		}
		if end, ok := obj["current_period_end"].(float64); ok && end > 0 {
			upd["current_period_end"] = time.Unix(int64(end), 0)
		}
		s.DB.Model(&gm.Subscription{}).Where("stripe_customer_id = ?", customer).Updates(upd)
	}
	s.DB.Exec(`UPDATE stripe_events SET processed_at = now() WHERE id = ?`, ev.ID)
	return "processed", nil
}
