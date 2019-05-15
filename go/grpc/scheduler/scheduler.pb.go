// Code generated by protoc-gen-go. DO NOT EDIT.
// source: scheduler/scheduler.proto

package scheduler

import (
	context "context"
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	grpc "google.golang.org/grpc"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

type CommitteeNode_Role int32

const (
	CommitteeNode_INVALID       CommitteeNode_Role = 0
	CommitteeNode_WORKER        CommitteeNode_Role = 1
	CommitteeNode_LEADER        CommitteeNode_Role = 2
	CommitteeNode_BACKUP_WORKER CommitteeNode_Role = 3
)

var CommitteeNode_Role_name = map[int32]string{
	0: "INVALID",
	1: "WORKER",
	2: "LEADER",
	3: "BACKUP_WORKER",
}

var CommitteeNode_Role_value = map[string]int32{
	"INVALID":       0,
	"WORKER":        1,
	"LEADER":        2,
	"BACKUP_WORKER": 3,
}

func (x CommitteeNode_Role) String() string {
	return proto.EnumName(CommitteeNode_Role_name, int32(x))
}

func (CommitteeNode_Role) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{0, 0}
}

type Committee_Kind int32

const (
	Committee_COMPUTE     Committee_Kind = 0
	Committee_STORAGE     Committee_Kind = 1
	Committee_TRANSACTION Committee_Kind = 2
)

var Committee_Kind_name = map[int32]string{
	0: "COMPUTE",
	1: "STORAGE",
	2: "TRANSACTION",
}

var Committee_Kind_value = map[string]int32{
	"COMPUTE":     0,
	"STORAGE":     1,
	"TRANSACTION": 2,
}

func (x Committee_Kind) String() string {
	return proto.EnumName(Committee_Kind_name, int32(x))
}

func (Committee_Kind) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{1, 0}
}

type CommitteeNode struct {
	PublicKey            []byte             `protobuf:"bytes,1,opt,name=public_key,json=publicKey,proto3" json:"public_key,omitempty"`
	Role                 CommitteeNode_Role `protobuf:"varint,2,opt,name=role,proto3,enum=scheduler.CommitteeNode_Role" json:"role,omitempty"`
	XXX_NoUnkeyedLiteral struct{}           `json:"-"`
	XXX_unrecognized     []byte             `json:"-"`
	XXX_sizecache        int32              `json:"-"`
}

func (m *CommitteeNode) Reset()         { *m = CommitteeNode{} }
func (m *CommitteeNode) String() string { return proto.CompactTextString(m) }
func (*CommitteeNode) ProtoMessage()    {}
func (*CommitteeNode) Descriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{0}
}

func (m *CommitteeNode) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_CommitteeNode.Unmarshal(m, b)
}
func (m *CommitteeNode) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_CommitteeNode.Marshal(b, m, deterministic)
}
func (m *CommitteeNode) XXX_Merge(src proto.Message) {
	xxx_messageInfo_CommitteeNode.Merge(m, src)
}
func (m *CommitteeNode) XXX_Size() int {
	return xxx_messageInfo_CommitteeNode.Size(m)
}
func (m *CommitteeNode) XXX_DiscardUnknown() {
	xxx_messageInfo_CommitteeNode.DiscardUnknown(m)
}

var xxx_messageInfo_CommitteeNode proto.InternalMessageInfo

func (m *CommitteeNode) GetPublicKey() []byte {
	if m != nil {
		return m.PublicKey
	}
	return nil
}

func (m *CommitteeNode) GetRole() CommitteeNode_Role {
	if m != nil {
		return m.Role
	}
	return CommitteeNode_INVALID
}

type Committee struct {
	Kind                 Committee_Kind   `protobuf:"varint,1,opt,name=kind,proto3,enum=scheduler.Committee_Kind" json:"kind,omitempty"`
	Members              []*CommitteeNode `protobuf:"bytes,2,rep,name=members,proto3" json:"members,omitempty"`
	RuntimeId            []byte           `protobuf:"bytes,3,opt,name=runtime_id,json=runtimeId,proto3" json:"runtime_id,omitempty"`
	ValidFor             uint64           `protobuf:"varint,4,opt,name=valid_for,json=validFor,proto3" json:"valid_for,omitempty"`
	XXX_NoUnkeyedLiteral struct{}         `json:"-"`
	XXX_unrecognized     []byte           `json:"-"`
	XXX_sizecache        int32            `json:"-"`
}

func (m *Committee) Reset()         { *m = Committee{} }
func (m *Committee) String() string { return proto.CompactTextString(m) }
func (*Committee) ProtoMessage()    {}
func (*Committee) Descriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{1}
}

func (m *Committee) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Committee.Unmarshal(m, b)
}
func (m *Committee) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Committee.Marshal(b, m, deterministic)
}
func (m *Committee) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Committee.Merge(m, src)
}
func (m *Committee) XXX_Size() int {
	return xxx_messageInfo_Committee.Size(m)
}
func (m *Committee) XXX_DiscardUnknown() {
	xxx_messageInfo_Committee.DiscardUnknown(m)
}

var xxx_messageInfo_Committee proto.InternalMessageInfo

func (m *Committee) GetKind() Committee_Kind {
	if m != nil {
		return m.Kind
	}
	return Committee_COMPUTE
}

func (m *Committee) GetMembers() []*CommitteeNode {
	if m != nil {
		return m.Members
	}
	return nil
}

func (m *Committee) GetRuntimeId() []byte {
	if m != nil {
		return m.RuntimeId
	}
	return nil
}

func (m *Committee) GetValidFor() uint64 {
	if m != nil {
		return m.ValidFor
	}
	return 0
}

type CommitteeRequest struct {
	RuntimeId            []byte   `protobuf:"bytes,1,opt,name=runtime_id,json=runtimeId,proto3" json:"runtime_id,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *CommitteeRequest) Reset()         { *m = CommitteeRequest{} }
func (m *CommitteeRequest) String() string { return proto.CompactTextString(m) }
func (*CommitteeRequest) ProtoMessage()    {}
func (*CommitteeRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{2}
}

func (m *CommitteeRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_CommitteeRequest.Unmarshal(m, b)
}
func (m *CommitteeRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_CommitteeRequest.Marshal(b, m, deterministic)
}
func (m *CommitteeRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_CommitteeRequest.Merge(m, src)
}
func (m *CommitteeRequest) XXX_Size() int {
	return xxx_messageInfo_CommitteeRequest.Size(m)
}
func (m *CommitteeRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_CommitteeRequest.DiscardUnknown(m)
}

var xxx_messageInfo_CommitteeRequest proto.InternalMessageInfo

func (m *CommitteeRequest) GetRuntimeId() []byte {
	if m != nil {
		return m.RuntimeId
	}
	return nil
}

type CommitteeResponse struct {
	Committee            []*Committee `protobuf:"bytes,1,rep,name=committee,proto3" json:"committee,omitempty"`
	XXX_NoUnkeyedLiteral struct{}     `json:"-"`
	XXX_unrecognized     []byte       `json:"-"`
	XXX_sizecache        int32        `json:"-"`
}

func (m *CommitteeResponse) Reset()         { *m = CommitteeResponse{} }
func (m *CommitteeResponse) String() string { return proto.CompactTextString(m) }
func (*CommitteeResponse) ProtoMessage()    {}
func (*CommitteeResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{3}
}

func (m *CommitteeResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_CommitteeResponse.Unmarshal(m, b)
}
func (m *CommitteeResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_CommitteeResponse.Marshal(b, m, deterministic)
}
func (m *CommitteeResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_CommitteeResponse.Merge(m, src)
}
func (m *CommitteeResponse) XXX_Size() int {
	return xxx_messageInfo_CommitteeResponse.Size(m)
}
func (m *CommitteeResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_CommitteeResponse.DiscardUnknown(m)
}

var xxx_messageInfo_CommitteeResponse proto.InternalMessageInfo

func (m *CommitteeResponse) GetCommittee() []*Committee {
	if m != nil {
		return m.Committee
	}
	return nil
}

type WatchRequest struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *WatchRequest) Reset()         { *m = WatchRequest{} }
func (m *WatchRequest) String() string { return proto.CompactTextString(m) }
func (*WatchRequest) ProtoMessage()    {}
func (*WatchRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{4}
}

func (m *WatchRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_WatchRequest.Unmarshal(m, b)
}
func (m *WatchRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_WatchRequest.Marshal(b, m, deterministic)
}
func (m *WatchRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_WatchRequest.Merge(m, src)
}
func (m *WatchRequest) XXX_Size() int {
	return xxx_messageInfo_WatchRequest.Size(m)
}
func (m *WatchRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_WatchRequest.DiscardUnknown(m)
}

var xxx_messageInfo_WatchRequest proto.InternalMessageInfo

type WatchResponse struct {
	Committee            *Committee `protobuf:"bytes,1,opt,name=committee,proto3" json:"committee,omitempty"`
	XXX_NoUnkeyedLiteral struct{}   `json:"-"`
	XXX_unrecognized     []byte     `json:"-"`
	XXX_sizecache        int32      `json:"-"`
}

func (m *WatchResponse) Reset()         { *m = WatchResponse{} }
func (m *WatchResponse) String() string { return proto.CompactTextString(m) }
func (*WatchResponse) ProtoMessage()    {}
func (*WatchResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_3d8db78ba60fec18, []int{5}
}

func (m *WatchResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_WatchResponse.Unmarshal(m, b)
}
func (m *WatchResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_WatchResponse.Marshal(b, m, deterministic)
}
func (m *WatchResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_WatchResponse.Merge(m, src)
}
func (m *WatchResponse) XXX_Size() int {
	return xxx_messageInfo_WatchResponse.Size(m)
}
func (m *WatchResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_WatchResponse.DiscardUnknown(m)
}

var xxx_messageInfo_WatchResponse proto.InternalMessageInfo

func (m *WatchResponse) GetCommittee() *Committee {
	if m != nil {
		return m.Committee
	}
	return nil
}

func init() {
	proto.RegisterEnum("scheduler.CommitteeNode_Role", CommitteeNode_Role_name, CommitteeNode_Role_value)
	proto.RegisterEnum("scheduler.Committee_Kind", Committee_Kind_name, Committee_Kind_value)
	proto.RegisterType((*CommitteeNode)(nil), "scheduler.CommitteeNode")
	proto.RegisterType((*Committee)(nil), "scheduler.Committee")
	proto.RegisterType((*CommitteeRequest)(nil), "scheduler.CommitteeRequest")
	proto.RegisterType((*CommitteeResponse)(nil), "scheduler.CommitteeResponse")
	proto.RegisterType((*WatchRequest)(nil), "scheduler.WatchRequest")
	proto.RegisterType((*WatchResponse)(nil), "scheduler.WatchResponse")
}

func init() { proto.RegisterFile("scheduler/scheduler.proto", fileDescriptor_3d8db78ba60fec18) }

var fileDescriptor_3d8db78ba60fec18 = []byte{
	// 469 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x8c, 0x93, 0xdf, 0x6e, 0xda, 0x4c,
	0x10, 0xc5, 0x59, 0xb0, 0x92, 0xcf, 0x43, 0x20, 0xce, 0xea, 0x93, 0xea, 0x24, 0x8d, 0x84, 0x7c,
	0xc5, 0x4d, 0xec, 0xe2, 0xde, 0x57, 0x72, 0x1c, 0x97, 0x22, 0x28, 0x44, 0x0b, 0x69, 0xa4, 0xde,
	0x20, 0x6c, 0x4f, 0x61, 0x85, 0xed, 0xa5, 0xfe, 0x53, 0x29, 0x8f, 0x53, 0xf5, 0xb9, 0xfa, 0x2e,
	0x95, 0x0d, 0x18, 0x9a, 0xba, 0x52, 0xef, 0x76, 0xce, 0x9c, 0x39, 0xfb, 0x9b, 0x95, 0x16, 0x2e,
	0x13, 0x6f, 0x85, 0x7e, 0x16, 0x60, 0x6c, 0x94, 0x27, 0x7d, 0x13, 0x8b, 0x54, 0x50, 0xb9, 0x14,
	0xb4, 0xef, 0x04, 0x5a, 0xb6, 0x08, 0x43, 0x9e, 0xa6, 0x88, 0x63, 0xe1, 0x23, 0xbd, 0x01, 0xd8,
	0x64, 0x6e, 0xc0, 0xbd, 0xf9, 0x1a, 0x9f, 0x55, 0xd2, 0x21, 0xdd, 0x33, 0x26, 0x6f, 0x95, 0x21,
	0x3e, 0xd3, 0x1e, 0x48, 0xb1, 0x08, 0x50, 0xad, 0x77, 0x48, 0xb7, 0x6d, 0xde, 0xe8, 0x87, 0xec,
	0xdf, 0x62, 0x74, 0x26, 0x02, 0x64, 0x85, 0x55, 0x7b, 0x07, 0x52, 0x5e, 0xd1, 0x26, 0x9c, 0x0e,
	0xc6, 0x9f, 0xac, 0xd1, 0xe0, 0x5e, 0xa9, 0x51, 0x80, 0x93, 0xa7, 0x09, 0x1b, 0x3a, 0x4c, 0x21,
	0xf9, 0x79, 0xe4, 0x58, 0xf7, 0x0e, 0x53, 0xea, 0xf4, 0x02, 0x5a, 0x77, 0x96, 0x3d, 0x7c, 0x7c,
	0x98, 0xef, 0xda, 0x0d, 0xed, 0x27, 0x01, 0xb9, 0x0c, 0xa7, 0xb7, 0x20, 0xad, 0x79, 0xe4, 0x17,
	0x64, 0x6d, 0xf3, 0xb2, 0x0a, 0x40, 0x1f, 0xf2, 0xc8, 0x67, 0x85, 0x8d, 0x9a, 0x70, 0x1a, 0x62,
	0xe8, 0x62, 0x9c, 0xa8, 0xf5, 0x4e, 0xa3, 0xdb, 0x34, 0xd5, 0xbf, 0x21, 0xb3, 0xbd, 0x31, 0x7f,
	0x82, 0x38, 0x8b, 0x52, 0x1e, 0xe2, 0x9c, 0xfb, 0x6a, 0x63, 0xfb, 0x04, 0x3b, 0x65, 0xe0, 0xd3,
	0x6b, 0x90, 0xbf, 0x2d, 0x02, 0xee, 0xcf, 0xbf, 0x88, 0x58, 0x95, 0x3a, 0xa4, 0x2b, 0xb1, 0xff,
	0x0a, 0xe1, 0xbd, 0x88, 0xb5, 0x1e, 0x48, 0xf9, 0xed, 0xf9, 0xb2, 0xf6, 0xe4, 0xe3, 0xc3, 0xe3,
	0xcc, 0x51, 0x6a, 0x79, 0x31, 0x9d, 0x4d, 0x98, 0xd5, 0x77, 0x14, 0x42, 0xcf, 0xa1, 0x39, 0x63,
	0xd6, 0x78, 0x6a, 0xd9, 0xb3, 0xc1, 0x64, 0xac, 0xd4, 0xb5, 0x1e, 0x28, 0x25, 0x08, 0xc3, 0xaf,
	0x19, 0x26, 0xe9, 0x0b, 0x04, 0xf2, 0x02, 0x41, 0xeb, 0xc3, 0xc5, 0xd1, 0x48, 0xb2, 0x11, 0x51,
	0x82, 0xd4, 0x04, 0xd9, 0xdb, 0x8b, 0x2a, 0x29, 0x96, 0xfd, 0xbf, 0x6a, 0x59, 0x76, 0xb0, 0x69,
	0x6d, 0x38, 0x7b, 0x5a, 0xa4, 0xde, 0x6a, 0x77, 0xaf, 0x66, 0x43, 0x6b, 0x57, 0x57, 0x87, 0x92,
	0x7f, 0x08, 0x35, 0x7f, 0x10, 0x90, 0xa7, 0x7b, 0x0b, 0x1d, 0x41, 0xab, 0x8f, 0x69, 0x69, 0x4c,
	0xe8, 0x75, 0xe5, 0xfc, 0x16, 0xe0, 0xea, 0x75, 0x75, 0x73, 0x4b, 0xa3, 0xd5, 0xe8, 0x07, 0x38,
	0x2f, 0x00, 0x8f, 0xf2, 0x5e, 0x1d, 0x8d, 0x1c, 0x2f, 0x73, 0xa5, 0xfe, 0xd9, 0xd8, 0xe7, 0xbc,
	0x21, 0x77, 0xc6, 0xe7, 0xdb, 0x25, 0x4f, 0x57, 0x99, 0xab, 0x7b, 0x22, 0x34, 0xc4, 0x22, 0xe1,
	0x49, 0xb0, 0x70, 0x13, 0x03, 0xd7, 0xdc, 0xc7, 0xc8, 0x58, 0x0a, 0x63, 0x19, 0x6f, 0xbc, 0xc3,
	0xe7, 0x71, 0x4f, 0x8a, 0xdf, 0xf3, 0xf6, 0x57, 0x00, 0x00, 0x00, 0xff, 0xff, 0x46, 0xfd, 0x79,
	0xd2, 0x5a, 0x03, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// SchedulerClient is the client API for Scheduler service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type SchedulerClient interface {
	GetCommittees(ctx context.Context, in *CommitteeRequest, opts ...grpc.CallOption) (*CommitteeResponse, error)
	WatchCommittees(ctx context.Context, in *WatchRequest, opts ...grpc.CallOption) (Scheduler_WatchCommitteesClient, error)
}

type schedulerClient struct {
	cc *grpc.ClientConn
}

func NewSchedulerClient(cc *grpc.ClientConn) SchedulerClient {
	return &schedulerClient{cc}
}

func (c *schedulerClient) GetCommittees(ctx context.Context, in *CommitteeRequest, opts ...grpc.CallOption) (*CommitteeResponse, error) {
	out := new(CommitteeResponse)
	err := c.cc.Invoke(ctx, "/scheduler.Scheduler/GetCommittees", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *schedulerClient) WatchCommittees(ctx context.Context, in *WatchRequest, opts ...grpc.CallOption) (Scheduler_WatchCommitteesClient, error) {
	stream, err := c.cc.NewStream(ctx, &_Scheduler_serviceDesc.Streams[0], "/scheduler.Scheduler/WatchCommittees", opts...)
	if err != nil {
		return nil, err
	}
	x := &schedulerWatchCommitteesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Scheduler_WatchCommitteesClient interface {
	Recv() (*WatchResponse, error)
	grpc.ClientStream
}

type schedulerWatchCommitteesClient struct {
	grpc.ClientStream
}

func (x *schedulerWatchCommitteesClient) Recv() (*WatchResponse, error) {
	m := new(WatchResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// SchedulerServer is the server API for Scheduler service.
type SchedulerServer interface {
	GetCommittees(context.Context, *CommitteeRequest) (*CommitteeResponse, error)
	WatchCommittees(*WatchRequest, Scheduler_WatchCommitteesServer) error
}

func RegisterSchedulerServer(s *grpc.Server, srv SchedulerServer) {
	s.RegisterService(&_Scheduler_serviceDesc, srv)
}

func _Scheduler_GetCommittees_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CommitteeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SchedulerServer).GetCommittees(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/scheduler.Scheduler/GetCommittees",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SchedulerServer).GetCommittees(ctx, req.(*CommitteeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Scheduler_WatchCommittees_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(WatchRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(SchedulerServer).WatchCommittees(m, &schedulerWatchCommitteesServer{stream})
}

type Scheduler_WatchCommitteesServer interface {
	Send(*WatchResponse) error
	grpc.ServerStream
}

type schedulerWatchCommitteesServer struct {
	grpc.ServerStream
}

func (x *schedulerWatchCommitteesServer) Send(m *WatchResponse) error {
	return x.ServerStream.SendMsg(m)
}

var _Scheduler_serviceDesc = grpc.ServiceDesc{
	ServiceName: "scheduler.Scheduler",
	HandlerType: (*SchedulerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetCommittees",
			Handler:    _Scheduler_GetCommittees_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "WatchCommittees",
			Handler:       _Scheduler_WatchCommittees_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "scheduler/scheduler.proto",
}
