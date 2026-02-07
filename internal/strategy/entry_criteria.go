// Package strategy provides trading strategies for the meme sniper bot.
// This file implements entry criteria filters.
package strategy

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// EntryCriteriaFilter filters tokens based on entry criteria.
type EntryCriteriaFilter struct {
	criteria *EntryCriteria
	logger   *zerolog.Logger
}

// EntryCriteriaFilterConfig holds configuration for entry criteria filtering.
type EntryCriteriaFilterConfig struct {
	Criteria *EntryCriteria
	Logger   *zerolog.Logger
}

// NewEntryCriteriaFilter creates a new entry criteria filter.
func NewEntryCriteriaFilter(config EntryCriteriaFilterConfig) *EntryCriteriaFilter {
	ecf := &EntryCriteriaFilter{
		criteria: config.Criteria,
		logger:   config.Logger,
	}

	// Set defaults
	if ecf.criteria == nil {
		ecf.criteria = &EntryCriteria{}
	}

	return ecf
}

// Evaluate evaluates whether a token meets entry criteria.
func (ecf *EntryCriteriaFilter) Evaluate(tokenInfo *TokenInfo) (*EntryResult, error) {
	if tokenInfo == nil {
		return &EntryResult{
			Passed:  false,
			Reason:  "token info is nil",
			Details: nil,
		}, nil
	}

	result := &EntryResult{
		Passed:  true,
		Reason:  "all criteria passed",
		Details: make(map[string]*CriteriaDetail),
	}

	// Check blacklist
	if detail := ecf.checkBlacklist(tokenInfo); !detail.Passed {
		result.Passed = false
		result.Reason = "token is blacklisted"
		result.Details["blacklist"] = detail
		return result, nil
	}
	result.Details["blacklist"] = &CriteriaDetail{Passed: true, Note: "not blacklisted"}

	// Check whitelist
	if detail := ecf.checkWhitelist(tokenInfo); !detail.Passed {
		result.Passed = false
		result.Reason = "token not in whitelist"
		result.Details["whitelist"] = detail
		return result, nil
	}
	result.Details["whitelist"] = &CriteriaDetail{Passed: true, Note: "in whitelist or no whitelist set"}

	// Check minimum liquidity
	{
		detail := ecf.checkMinLiquidity(tokenInfo)
		result.Details["liquidity"] = detail
		if !detail.Passed {
			result.Passed = false
			result.Reason = "insufficient liquidity"
			return result, nil
		}
	}

	// Check holder concentration
	{
		detail := ecf.checkHolderConcentration(tokenInfo)
		result.Details["holder_concentration"] = detail
		if !detail.Passed {
			result.Passed = false
			result.Reason = "holder concentration too high"
			return result, nil
		}
	}

	// Check minimum score
	{
		detail := ecf.checkMinScore(tokenInfo)
		result.Details["score"] = detail
		if !detail.Passed {
			result.Passed = false
			result.Reason = "score below minimum"
			return result, nil
		}
	}

	// Check socials
	{
		detail := ecf.checkSocials(tokenInfo)
		result.Details["socials"] = detail
		if !detail.Passed {
			result.Passed = false
			result.Reason = "socials required but not found"
			return result, nil
		}
	}

	// Check age
	{
		detail := ecf.checkAge(tokenInfo)
		result.Details["age"] = detail
		if !detail.Passed {
			result.Passed = false
			result.Reason = "token too old"
			return result, nil
		}
	}

	// Check market cap
	{
		detail := ecf.checkMarketCap(tokenInfo)
		result.Details["market_cap"] = detail
		if !detail.Passed {
			result.Passed = false
			result.Reason = "market cap outside range"
			return result, nil
		}
	}

	return result, nil
}

// EntryResult represents the result of entry criteria evaluation.
type EntryResult struct {
	Passed  bool                       `json:"passed"`
	Reason  string                     `json:"reason"`
	Details map[string]*CriteriaDetail `json:"details"`
}

// CriteriaDetail represents detail about a specific criteria check.
type CriteriaDetail struct {
	Passed   bool            `json:"passed"`
	Value    decimal.Decimal `json:"value,omitempty"`
	Required decimal.Decimal `json:"required,omitempty"`
	Note     string          `json:"note,omitempty"`
}

// checkBlacklist checks if token is blacklisted.
func (ecf *EntryCriteriaFilter) checkBlacklist(tokenInfo *TokenInfo) *CriteriaDetail {
	if len(ecf.criteria.Blacklist) == 0 {
		return &CriteriaDetail{Passed: true, Note: "no blacklist configured"}
	}

	// Check exact address match
	for _, addr := range ecf.criteria.Blacklist {
		if strings.EqualFold(tokenInfo.Address, addr) {
			return &CriteriaDetail{
				Passed: false,
				Note:   fmt.Sprintf("address %s in blacklist", addr),
			}
		}
	}

	// Check symbol match
	for _, symbol := range ecf.criteria.Blacklist {
		if strings.EqualFold(tokenInfo.Symbol, symbol) {
			return &CriteriaDetail{
				Passed: false,
				Note:   fmt.Sprintf("symbol %s in blacklist", symbol),
			}
		}
	}

	return &CriteriaDetail{Passed: true, Note: "not in blacklist"}
}

// checkWhitelist checks if token is whitelisted (if whitelist is enabled).
func (ecf *EntryCriteriaFilter) checkWhitelist(tokenInfo *TokenInfo) *CriteriaDetail {
	if len(ecf.criteria.Whitelist) == 0 {
		return &CriteriaDetail{Passed: true, Note: "no whitelist configured"}
	}

	// Check address match
	for _, addr := range ecf.criteria.Whitelist {
		if strings.EqualFold(tokenInfo.Address, addr) {
			return &CriteriaDetail{Passed: true, Note: "address in whitelist"}
		}
	}

	// Check symbol match
	for _, symbol := range ecf.criteria.Whitelist {
		if strings.EqualFold(tokenInfo.Symbol, symbol) {
			return &CriteriaDetail{Passed: true, Note: "symbol in whitelist"}
		}
	}

	return &CriteriaDetail{
		Passed: false,
		Note:   "not in whitelist",
	}
}

// checkMinLiquidity checks minimum liquidity requirement.
func (ecf *EntryCriteriaFilter) checkMinLiquidity(tokenInfo *TokenInfo) *CriteriaDetail {
	if ecf.criteria.MinLiquidity.IsZero() {
		return &CriteriaDetail{Passed: true, Note: "no minimum liquidity configured"}
	}

	if tokenInfo.Liquidity.IsZero() {
		return &CriteriaDetail{
			Passed:   false,
			Required: ecf.criteria.MinLiquidity,
			Note:     "liquidity data unavailable",
		}
	}

	if tokenInfo.Liquidity.GreaterThanOrEqual(ecf.criteria.MinLiquidity) {
		return &CriteriaDetail{
			Passed:   true,
			Value:    tokenInfo.Liquidity,
			Required: ecf.criteria.MinLiquidity,
			Note:     "meets minimum liquidity",
		}
	}

	return &CriteriaDetail{
		Passed:   false,
		Value:    tokenInfo.Liquidity,
		Required: ecf.criteria.MinLiquidity,
		Note:     "below minimum liquidity",
	}
}

// checkHolderConcentration checks holder concentration.
func (ecf *EntryCriteriaFilter) checkHolderConcentration(tokenInfo *TokenInfo) *CriteriaDetail {
	if ecf.criteria.MaxHolderConcentration.IsZero() {
		return &CriteriaDetail{Passed: true, Note: "no holder concentration limit"}
	}

	if len(tokenInfo.Holders) == 0 {
		return &CriteriaDetail{Passed: true, Note: "holder data unavailable"}
	}

	// Calculate concentration of top holders
	var topHolderPercent decimal.Decimal
	for i, holder := range tokenInfo.Holders {
		if i >= 10 { // Check top 10 holders
			break
		}
		topHolderPercent = topHolderPercent.Add(holder.Percent)
	}

	if topHolderPercent.LessThanOrEqual(ecf.criteria.MaxHolderConcentration) {
		return &CriteriaDetail{
			Passed:   true,
			Value:    topHolderPercent,
			Required: ecf.criteria.MaxHolderConcentration,
			Note:     "holder concentration acceptable",
		}
	}

	return &CriteriaDetail{
		Passed:   false,
		Value:    topHolderPercent,
		Required: ecf.criteria.MaxHolderConcentration,
		Note:     "holder concentration too high",
	}
}

// checkMinScore checks minimum score requirement.
func (ecf *EntryCriteriaFilter) checkMinScore(tokenInfo *TokenInfo) *CriteriaDetail {
	if ecf.criteria.MinScore == 0 {
		return &CriteriaDetail{Passed: true, Note: "no minimum score configured"}
	}

	if tokenInfo.Score >= ecf.criteria.MinScore {
		return &CriteriaDetail{
			Passed: true,
			Note:   fmt.Sprintf("score %d meets minimum %d", tokenInfo.Score, ecf.criteria.MinScore),
		}
	}

	return &CriteriaDetail{
		Passed: false,
		Note:   fmt.Sprintf("score %d below minimum %d", tokenInfo.Score, ecf.criteria.MinScore),
	}
}

// checkSocials checks social media requirements.
func (ecf *EntryCriteriaFilter) checkSocials(tokenInfo *TokenInfo) *CriteriaDetail {
	if !ecf.criteria.RequireSocials {
		return &CriteriaDetail{Passed: true, Note: "socials not required"}
	}

	if tokenInfo.Socials == nil {
		return &CriteriaDetail{
			Passed: false,
			Note:   "no social information available",
		}
	}

	hasSocial := false
	var socialList []string

	if tokenInfo.Socials.Twitter != "" {
		hasSocial = true
		socialList = append(socialList, "twitter")
	}
	if tokenInfo.Socials.Telegram != "" {
		hasSocial = true
		socialList = append(socialList, "telegram")
	}
	if tokenInfo.Socials.Discord != "" {
		hasSocial = true
		socialList = append(socialList, "discord")
	}
	if tokenInfo.Socials.Website != "" {
		hasSocial = true
		socialList = append(socialList, "website")
	}

	if hasSocial {
		return &CriteriaDetail{
			Passed: true,
			Note:   fmt.Sprintf("has socials: %v", socialList),
		}
	}

	return &CriteriaDetail{
		Passed: false,
		Note:   "no social media found",
	}
}

// checkAge checks token age.
func (ecf *EntryCriteriaFilter) checkAge(tokenInfo *TokenInfo) *CriteriaDetail {
	if ecf.criteria.MaxAge == 0 {
		return &CriteriaDetail{Passed: true, Note: "no age limit configured"}
	}

	if tokenInfo.LaunchTime.IsZero() {
		return &CriteriaDetail{Passed: true, Note: "launch time unavailable"}
	}

	age := time.Since(tokenInfo.LaunchTime)

	if age <= ecf.criteria.MaxAge {
		return &CriteriaDetail{
			Passed: true,
			Note:   fmt.Sprintf("age %s within limit %s", age, ecf.criteria.MaxAge),
		}
	}

	return &CriteriaDetail{
		Passed: false,
		Note:   fmt.Sprintf("age %s exceeds limit %s", age, ecf.criteria.MaxAge),
	}
}

// checkMarketCap checks market cap range.
func (ecf *EntryCriteriaFilter) checkMarketCap(tokenInfo *TokenInfo) *CriteriaDetail {
	hasMin := !ecf.criteria.MinMarketCap.IsZero()
	hasMax := !ecf.criteria.MaxMarketCap.IsZero()

	if !hasMin && !hasMax {
		return &CriteriaDetail{Passed: true, Note: "no market cap limits configured"}
	}

	if tokenInfo.MarketCap.IsZero() {
		return &CriteriaDetail{
			Passed: false,
			Note:   "market cap unavailable",
		}
	}

	if hasMin && tokenInfo.MarketCap.LessThan(ecf.criteria.MinMarketCap) {
		return &CriteriaDetail{
			Passed:   false,
			Value:    tokenInfo.MarketCap,
			Required: ecf.criteria.MinMarketCap,
			Note:     "below minimum market cap",
		}
	}

	if hasMax && tokenInfo.MarketCap.GreaterThan(ecf.criteria.MaxMarketCap) {
		return &CriteriaDetail{
			Passed:   false,
			Value:    tokenInfo.MarketCap,
			Required: ecf.criteria.MaxMarketCap,
			Note:     "above maximum market cap",
		}
	}

	return &CriteriaDetail{
		Passed: true,
		Value:  tokenInfo.MarketCap,
		Note:   "market cap within range",
	}
}

// ValidateConfig validates the entry criteria configuration.
func (ecf *EntryCriteriaFilter) ValidateConfig() error {
	if ecf.criteria == nil {
		return nil
	}

	// Validate market cap range
	if !ecf.criteria.MinMarketCap.IsZero() && !ecf.criteria.MaxMarketCap.IsZero() {
		if ecf.criteria.MinMarketCap.GreaterThan(ecf.criteria.MaxMarketCap) {
			return fmt.Errorf("min market cap %s greater than max %s",
				ecf.criteria.MinMarketCap, ecf.criteria.MaxMarketCap)
		}
	}

	// Validate holder concentration
	if ecf.criteria.MaxHolderConcentration.IsNegative() {
		return errors.New("holder concentration cannot be negative")
	}

	if ecf.criteria.MaxHolderConcentration.GreaterThan(decimal.NewFromInt(100)) {
		return errors.New("holder concentration cannot exceed 100%%")
	}

	// Validate score
	if ecf.criteria.MinScore < 0 || ecf.criteria.MinScore > 100 {
		return errors.New("min score must be between 0 and 100")
	}

	return nil
}

// UpdateCriteria updates the entry criteria.
func (ecf *EntryCriteriaFilter) UpdateCriteria(criteria *EntryCriteria) error {
	if err := ecf.validateCriteriaInternal(criteria); err != nil {
		return err
	}

	ecf.criteria = criteria

	if ecf.logger != nil {
		ecf.logger.Info().
			Interface("criteria", criteria).
			Msg("Entry criteria updated")
	}

	return nil
}

// validateCriteriaInternal validates a criteria configuration.
func (ecf *EntryCriteriaFilter) validateCriteriaInternal(criteria *EntryCriteria) error {
	if criteria == nil {
		return nil
	}

	// Validate market cap range
	if !criteria.MinMarketCap.IsZero() && !criteria.MaxMarketCap.IsZero() {
		if criteria.MinMarketCap.GreaterThan(criteria.MaxMarketCap) {
			return errors.New("invalid market cap range")
		}
	}

	if criteria.MaxHolderConcentration.LessThan(decimal.Zero) ||
		criteria.MaxHolderConcentration.GreaterThan(decimal.NewFromInt(100)) {
		return errors.New("invalid holder concentration")
	}

	if criteria.MinScore < 0 || criteria.MinScore > 100 {
		return errors.New("invalid min score")
	}

	return nil
}

// GetCriteria returns the current entry criteria.
func (ecf *EntryCriteriaFilter) GetCriteria() *EntryCriteria {
	return ecf.criteria
}

// BatchEvaluate evaluates multiple tokens.
func (ecf *EntryCriteriaFilter) BatchEvaluate(tokens []*TokenInfo) []*EntryResult {
	results := make([]*EntryResult, len(tokens))

	for i, token := range tokens {
		result, err := ecf.Evaluate(token)
		if err != nil {
			results[i] = &EntryResult{
				Passed: false,
				Reason: fmt.Sprintf("evaluation error: %v", err),
			}
			if ecf.logger != nil {
				ecf.logger.Error().
					Err(err).
					Str("token", token.Symbol).
					Msg("Error evaluating entry criteria")
			}
		} else {
			results[i] = result
		}
	}

	return results
}

// FilterTokens filters a list of tokens, returning only those that pass.
func (ecf *EntryCriteriaFilter) FilterTokens(tokens []*TokenInfo) []*TokenInfo {
	var passed []*TokenInfo

	for _, token := range tokens {
		result, err := ecf.Evaluate(token)
		if err != nil {
			continue
		}
		if result.Passed {
			passed = append(passed, token)
		}
	}

	return passed
}

// GetPassRate calculates the pass rate for a batch of tokens.
func (ecf *EntryCriteriaFilter) GetPassRate(tokens []*TokenInfo) (int, int, decimal.Decimal) {
	if len(tokens) == 0 {
		return 0, 0, decimal.Zero
	}

	passedCount := 0
	for _, token := range tokens {
		result, err := ecf.Evaluate(token)
		if err == nil && result.Passed {
			passedCount++
		}
	}

	passRate := decimal.NewFromInt(int64(passedCount)).
		Div(decimal.NewFromInt(int64(len(tokens)))).
		Mul(decimal.NewFromInt(100))

	return passedCount, len(tokens), passRate
}

// AddToBlacklist adds an address or symbol to the blacklist.
func (ecf *EntryCriteriaFilter) AddToBlacklist(item string) {
	for _, existing := range ecf.criteria.Blacklist {
		if strings.EqualFold(existing, item) {
			return // Already in blacklist
		}
	}

	ecf.criteria.Blacklist = append(ecf.criteria.Blacklist, item)

	if ecf.logger != nil {
		ecf.logger.Info().
			Str("item", item).
			Msg("Added to blacklist")
	}
}

// AddToWhitelist adds an address or symbol to the whitelist.
func (ecf *EntryCriteriaFilter) AddToWhitelist(item string) {
	for _, existing := range ecf.criteria.Whitelist {
		if strings.EqualFold(existing, item) {
			return // Already in whitelist
		}
	}

	ecf.criteria.Whitelist = append(ecf.criteria.Whitelist, item)

	if ecf.logger != nil {
		ecf.logger.Info().
			Str("item", item).
			Msg("Added to whitelist")
	}
}

// RemoveFromBlacklist removes an item from the blacklist.
func (ecf *EntryCriteriaFilter) RemoveFromBlacklist(item string) {
	newList := make([]string, 0, len(ecf.criteria.Blacklist))
	for _, existing := range ecf.criteria.Blacklist {
		if !strings.EqualFold(existing, item) {
			newList = append(newList, existing)
		}
	}
	ecf.criteria.Blacklist = newList

	if ecf.logger != nil {
		ecf.logger.Info().
			Str("item", item).
			Msg("Removed from blacklist")
	}
}

// RemoveFromWhitelist removes an item from the whitelist.
func (ecf *EntryCriteriaFilter) RemoveFromWhitelist(item string) {
	newList := make([]string, 0, len(ecf.criteria.Whitelist))
	for _, existing := range ecf.criteria.Whitelist {
		if !strings.EqualFold(existing, item) {
			newList = append(newList, existing)
		}
	}
	ecf.criteria.Whitelist = newList

	if ecf.logger != nil {
		ecf.logger.Info().
			Str("item", item).
			Msg("Removed from whitelist")
	}
}
