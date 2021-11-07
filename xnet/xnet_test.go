package xnet

import (
	"os"
	"pb"
	"testing"
	"xcore/xlog"
)

var msgIDMap = map[pb.Game_Msg]int{
	pb.MSG_Input: 200000,
}

var targetBin []byte
var err error

func init() {
	pbFact = createFact()
	RegisterPBMsgID(uint16(pb.MSG_Input), (*pb.C2B_Input)(nil))
	proto := &pb.C2B_Input{Input: []byte{1, 2, 2, 3}}
	targetBin, err = proto.Marshal()
	if err != nil {
		xlog.Errorf("init Marshal err=%v", err)
		os.Exit(1)
	}
}

func BenchmarkPBTest(b *testing.B) {
	//proto := &pb.C2B_Input{}
	for msgID := range msgIDMap {
		for i := 0; i < b.N; i++ {
			pack := pbFact.createByMsgID(uint16(msgID))
			if pack == nil {
				xlog.Errorf("nil msgID=%d", msgID)
			}
			err = pack.Body.(*pb.C2B_Input).Unmarshal(targetBin)
			//err = proto.Unmarshal(targetBin)
			if err != nil {
				xlog.Errorf("Unmarshal err=%v", err)
			}
			pbFact.freePacket(pack)
		}
	}
}
