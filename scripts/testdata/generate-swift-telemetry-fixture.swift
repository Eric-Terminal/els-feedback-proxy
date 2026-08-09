#!/usr/bin/env swift

import CryptoKit
import Foundation

// 使用与 App 相同的“排序键 + 不转义斜杠”规则生成跨语言验收数据。
let jsonOptions: JSONSerialization.WritingOptions = [.sortedKeys, .withoutEscapingSlashes]
let now = Date()
let formatter = ISO8601DateFormatter()

func makeEnvelope(kind: String, payload: [String: Any]) throws -> [String: Any] {
    let payloadData = try JSONSerialization.data(withJSONObject: payload, options: jsonOptions)
    let payloadID = SHA256.hash(data: payloadData)
        .map { String(format: "%02x", $0) }
        .joined()

    return [
        "schema_version": 2,
        "payload_id": payloadID,
        "kind": kind,
        "captured_at": formatter.string(from: now),
        "period_start": formatter.string(from: now.addingTimeInterval(-86_400)),
        "period_end": formatter.string(from: now),
        "app": [
            "version": "2.7.0",
            "build": "e2e-270",
            "distribution": "testflight"
        ],
        "platform": [
            "name": "ios",
            "os_version": "26.0",
            "device_class": "iPhone17,2",
            "architecture": "arm64"
        ],
        "privacy": [
            "contains_chat_content": false,
            "contains_request_body": false,
            "contains_response_body": false,
            "contains_credentials": false,
            "contains_user_identifier": false
        ],
        "payload": payload
    ]
}

let metricPayload: [String: Any] = [
    "_etos": [
        "format": "metric-kit-flat-v1",
        "call_stack_frames_emitted": 0,
        "truncated": false
    ],
    "signpostMetrics": [
        [
            "signpostCategory": "Network",
            "signpostName": "ModelRequestStreaming",
            "totalCount": 4,
            "signpostIntervalData": [
                "histogrammedSignpostDuration": [
                    "histogram": [
                        ["bucketStart": "0 ms", "bucketEnd": "10 ms", "bucketCount": 1],
                        ["bucketStart": "10 ms", "bucketEnd": "20 ms", "bucketCount": 2],
                        ["bucketStart": "20 ms", "bucketEnd": "30 ms", "bucketCount": 1]
                    ]
                ]
            ]
        ]
    ]
]
let diagnosticPayload: [String: Any] = [
    "_etos": [
        "format": "metric-kit-flat-v1",
        "call_stack_frames_emitted": 1,
        "truncated": false
    ],
    "hangDiagnostics": [
        [
            "hangDuration": ["value": 2, "unit": "s"],
            "callStackTree": [
                "format": "flat-v1",
                "callStackPerThread": true,
                "truncated": false,
                "callStacks": [
                    [
                        "threadAttributed": true,
                        "callStackFrames": [
                            [
                                "frameID": 0,
                                "depth": 0,
                                "binaryName": "ETOS LLM Studio",
                                "binaryUUID": "70B89F27-1634-3580-A695-57CDB41D7743",
                                "address": 4_294_971_392,
                                "offsetIntoBinaryTextSegment": 4_096,
                                "sampleCount": 1
                            ]
                        ]
                    ]
                ]
            ]
        ]
    ]
]

let request: [String: Any] = [
    "schema_version": 2,
    "envelopes": [
        try makeEnvelope(kind: "metric", payload: metricPayload),
        try makeEnvelope(kind: "diagnostic", payload: diagnosticPayload)
    ]
]
let data = try JSONSerialization.data(withJSONObject: request, options: jsonOptions)
FileHandle.standardOutput.write(data)
FileHandle.standardOutput.write(Data([0x0A]))
