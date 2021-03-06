package gophermart_test

import (
	"testing"

	"github.com/vanamelnik/gophermart/storage"
	mockstorage "github.com/vanamelnik/gophermart/storage/mock"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithdraw(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	db := mockstorage.NewMockStorage(mockCtrl)
	ctx, s, err := initServices(db, pepper)
	require.NoError(t, err)

	tt := []struct {
		name       string
		mockReturn error
		wantErr    error
	}{
		{
			name:       "#1 Not enough points",
			mockReturn: storage.ErrInsufficientPoints,
			wantErr:    storage.ErrInsufficientPoints,
		},
		{
			name:       "#2 Normal case",
			mockReturn: nil,
			wantErr:    nil,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			db.EXPECT().ProcessWithdraw(gomock.Any(), gomock.Any()).Return(tc.mockReturn).Times(1)
			assert.ErrorIs(t, s.Withdraw(ctx, "00", 123.45), tc.wantErr)
		})
	}
}
