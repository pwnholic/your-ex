package solana

// TokenMetadata represents token metadata from the mint account.
type TokenMetadata struct {
	Name            string
	Symbol          string
	Decimals        uint8
	URI             string
	Supply          string
	MintAuthority   *string
	FreezeAuthority *string
	Twitter         string
	Telegram        string
	Website         string
}
