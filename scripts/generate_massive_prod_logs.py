#!/usr/bin/env python3
"""
Generates massive, production-grade cloud-native microservice logs
with realistic background noise, distributed tracing, gradual resource
degradation, and authentic failure cascades.
"""
import os
import random
import uuid
from datetime import datetime, timedelta

DEST_DIR = "/home/vj/code/log_analyzer/testdata/production_logs"
os.makedirs(DEST_DIR, exist_ok=True)

START_TIME = datetime(2026, 10, 27, 4, 0, 0)

def random_trace():
    tid = uuid.uuid4().hex[:16]
    sid = uuid.uuid4().hex[:8]
    return f"[traceId={tid},spanId={sid}]"

# ==============================================================================
# 1. Spring Boot Payment Service OOM Under Heavy Batch Load (~5,000 lines)
# ==============================================================================
def generate_payment_oom_log():
    filepath = os.path.join(DEST_DIR, "prod-k8s-payment-service-oom-storm.log")
    curr_time = START_TIME
    lines = []
    
    lines.append(f"{curr_time.isoformat()}Z [INFO] [main] org.springframework.boot.Startup - Starting PaymentService v3.2.0 on pod payment-svc-prod-7d4bf9-89klw")
    lines.append(f"{curr_time.isoformat()}Z [INFO] [main] o.s.b.w.embedded.tomcat.TomcatWebServer - Tomcat started on port 8080 (http) with context path ''")
    lines.append(f"{curr_time.isoformat()}Z [INFO] [main] c.e.payment.Application - Started in 4.128 seconds (JVM running for 4.891)")
    
    # 4,500 normal routine transactions with healthy GC
    for i in range(1, 4501):
        curr_time += timedelta(milliseconds=random.randint(10, 60))
        tstr = curr_time.isoformat() + "Z"
        tr = random_trace()
        cid = f"CUST-{random.randint(10000, 99999)}"
        oid = f"ORD-{random.randint(100000, 999999)}"
        amt = f"{random.uniform(10.0, 500.0):.2f}"
        
        lines.append(f"{tstr} [INFO] [http-nio-8080-exec-{random.randint(1, 30)}] {tr} c.e.p.c.PaymentController - Inbound POST /api/v2/charge customer={cid} order={oid} amt=${amt}")
        
        if random.random() < 0.3:
            lines.append(f"{tstr} [DEBUG] [http-nio-8080-exec-{random.randint(1, 30)}] {tr} c.e.p.s.FraudEngine - Risk score evaluated: 0.04 (APPROVED)")
        if random.random() < 0.2:
            lines.append(f"{tstr} [DEBUG] [http-nio-8080-exec-{random.randint(1, 30)}] {tr} org.hibernate.SQL - select a1_0.id,a1_0.balance from accounts a1_0 where a1_0.customer_id=?")
        
        lines.append(f"{tstr} [INFO] [http-nio-8080-exec-{random.randint(1, 30)}] {tr} c.e.p.c.PaymentController - Response 200 OK duration={random.randint(12, 45)}ms")
        
        if i % 500 == 0:
            lines.append(f"{tstr} [INFO] [G1-Young-GC] org.openjdk.jvm.gc - G1 Evacuation Pause (young) duration=18ms heap_used=312MB heap_max=2048MB")
            lines.append(f"{tstr} [INFO] [health-check] c.e.p.m.HealthIndicator - GET /actuator/health HTTP/1.1 200 UP db=UP disk=UP redis=UP")

    # The disaster begins: heavy reconciliation batch ingest
    curr_time += timedelta(seconds=2)
    tstr = curr_time.isoformat() + "Z"
    lines.append(f"{tstr} [WARN] [batch-executor-1] c.e.p.s.BatchReconciliationService - INGESTION WARNING: Massive reconciliation chunk received: 2,500,000 ledger records (size: 980 MB)")
    
    for i in range(1, 300):
        curr_time += timedelta(milliseconds=random.randint(50, 150))
        tstr = curr_time.isoformat() + "Z"
        tr = random_trace()
        lines.append(f"{tstr} [WARN] [G1-Young-GC] org.openjdk.jvm.gc - G1 Evacuation Pause (young) duration={200 + i*5}ms heap_used={1600 + i}MB heap_max=2048MB")
        lines.append(f"{tstr} [INFO] [http-nio-8080-exec-{random.randint(1, 30)}] {tr} c.e.p.c.PaymentController - Slow transaction detected: latency={800 + i*10}ms")
    
    curr_time += timedelta(seconds=1)
    tstr = curr_time.isoformat() + "Z"
    lines.append(f"{tstr} [ERROR] [G1-Old-GC] org.openjdk.jvm.gc - G1 Full GC cycle triggered; pause=6890ms reclaimed=8MB (heap exhaustion)")
    lines.append(f"{tstr} [ERROR] [http-nio-8080-exec-19] o.a.c.c.C.[.[.[/].[dispatcherServlet] - Servlet.service() for servlet [dispatcherServlet] threw exception:")
    lines.append("java.lang.OutOfMemoryError: Java heap space")
    lines.append("\tat java.base/java.util.Arrays.copyOf(Arrays.java:3537)")
    lines.append("\tat java.base/java.io.ByteArrayOutputStream.grow(ByteArrayOutputStream.java:120)")
    lines.append("\tat java.base/java.io.ByteArrayOutputStream.write(ByteArrayOutputStream.java:155)")
    lines.append("\tat com.fasterxml.jackson.core.util.ByteArrayBuilder.write(ByteArrayBuilder.java:185)")
    lines.append("\tat com.fasterxml.jackson.databind.ObjectMapper.writeValueAsBytes(ObjectMapper.java:3865)")
    lines.append("\tat com.example.payment.service.BatchReconciliationService.reconcile(BatchReconciliationService.java:142)")
    lines.append(f"{tstr} [FATAL] [health-watchdog] c.e.p.m.HealthIndicator - CRITICAL: Memory utilization at 99.8%. JVM heap failed to allocate 65536 bytes")
    
    curr_time += timedelta(seconds=1)
    tstr = curr_time.isoformat() + "Z"
    lines.append(f"{tstr} kernel: [102931.401923] oom-killer: gfp_mask=0x1100cca(GFP_HIGHUSER_MOVABLE), order=0")
    lines.append(f"{tstr} kernel: [102931.401928] Memory cgroup out of memory: Killed process 3140 (java) total-vm:4194304kB, anon-rss:2097152kB, file-rss:0kB, shmem-rss:0kB")
    lines.append(f"{tstr} kubelet: Container payment-service in pod payment-svc-prod-7d4bf9-89klw terminated with exit code 137 (OOMKilled)")
    
    with open(filepath, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"Generated {filepath} ({len(lines)} lines)")

# ==============================================================================
# 2. Golang High-Concurrency Nil-Pointer Panic under Traffic Surge (~5,000 lines)
# ==============================================================================
def generate_go_panic_log():
    filepath = os.path.join(DEST_DIR, "prod-k8s-ecommerce-panic-under-load.log")
    curr_time = START_TIME
    lines = []
    
    lines.append(f'{{"level":"info","ts":"{curr_time.isoformat()}Z","caller":"main.go:45","msg":"e-commerce-api starting on 0.0.0.0:8080","env":"production"}}')
    lines.append(f'{{"level":"info","ts":"{curr_time.isoformat()}Z","caller":"router.go:90","msg":"Registered 34 HTTP route handlers"}}')
    
    endpoints = ["/api/v1/cart", "/api/v1/products", "/api/v1/categories", "/api/v1/search", "/api/v1/recommendations"]
    
    for i in range(1, 4801):
        curr_time += timedelta(milliseconds=random.randint(5, 40))
        tstr = curr_time.isoformat() + "Z"
        ep = random.choice(endpoints)
        req_id = uuid.uuid4().hex[:12]
        user_id = random.randint(1000, 99999)
        dur = random.uniform(1.2, 14.5)
        
        lines.append(f'{{"level":"info","ts":"{tstr}","caller":"middleware/logger.go:34","req_id":"{req_id}","method":"GET","path":"{ep}","status":200,"duration_ms":{dur:.2f},"user_id":{user_id}}}')
        if random.random() < 0.15:
            lines.append(f'{{"level":"debug","ts":"{tstr}","caller":"cache/redis.go:78","req_id":"{req_id}","cache_hit":true,"key":"catalog:{user_id}"}}')
        if i % 600 == 0:
            lines.append(f'{{"level":"info","ts":"{tstr}","caller":"metrics/reporter.go:50","goroutines":{random.randint(120, 250)},"heap_alloc_mb":{random.randint(80, 140)},"status":"HEALTHY"}}')

    # Sudden malformed session payload triggers edge-case nil pointer
    curr_time += timedelta(milliseconds=20)
    tstr = curr_time.isoformat() + "Z"
    bad_req_id = "req-malformed-9988"
    lines.append(f'{{"level":"warn","ts":"{tstr}","caller":"auth/jwt.go:112","req_id":"{bad_req_id}","msg":"Received anonymous guest session with missing UserProfile struct"}}')
    lines.append(f'{{"level":"info","ts":"{tstr}","caller":"service/checkout.go:55","req_id":"{bad_req_id}","msg":"Attempting to evaluate loyalty tier on uninitialized session"}}')
    
    panic_trace = """panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x28 pc=0x83e2a1]

goroutine 4129 [running]:
github.com/example/ecommerce-api/pkg/loyalty.(*TierService).CalculateTierPoints(0x0, 0xc00210e400)
	/workspace/pkg/loyalty/tier.go:88 +0x34
github.com/example/ecommerce-api/pkg/service.(*CheckoutService).ProcessOrder(0xc00018a300, {0x94b120, 0xc00192e000}, 0xc00210e400)
	/workspace/pkg/service/checkout.go:142 +0x21a
github.com/example/ecommerce-api/pkg/handler.(*CheckoutHandler).HandleCheckout(0xc00018a380, {0x94b120, 0xc00192e000}, 0xc00210e480)
	/workspace/pkg/handler/checkout.go:74 +0x189
net/http.HandlerFunc.ServeHTTP(0xc0001e0120, {0x94c200, 0xc002220e00}, 0xc00210e480)
	/usr/local/go/src/net/http/server.go:2136 +0x29
github.com/gorilla/mux.(*Router).ServeHTTP(0xc0001c2300, {0x94c200, 0xc002220e00}, 0xc00210e480)
	/go/pkg/mod/github.com/gorilla/mux@v1.8.1/mux.go:212 +0x1cf
net/http.serverHandler.ServeHTTP({0xc0001be000}, {0x94c200, 0xc002220e00}, 0xc00210e480)
	/usr/local/go/src/net/http/server.go:2938 +0x8e
net/http.(*conn).serve(0xc002181b00, {0x94c800, 0xc000194840})
	/usr/local/go/src/net/http/server.go:2009 +0x5f8
created by net/http.(*Server).Serve in goroutine 1
	/usr/local/go/src/net/http/server.go:3086 +0x5d4"""
    lines.extend(panic_trace.split("\n"))
    lines.append(f'{{"level":"fatal","ts":"{tstr}","caller":"runtime","msg":"Process terminated abruptly with signal SIGSEGV (exit code 2)"}}')
    lines.append(f'{tstr} kubelet: Container ecommerce-api in pod ecommerce-api-7b89f-2m8kp terminated with exit code 2')

    with open(filepath, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"Generated {filepath} ({len(lines)} lines)")

# ==============================================================================
# 3. Envoy Ingress Gateway Downstream Database Cascading 504 Timeout (~5,000 lines)
# ==============================================================================
def generate_envoy_timeout_log():
    filepath = os.path.join(DEST_DIR, "prod-k8s-envoy-gateway-cascade-504.log")
    curr_time = START_TIME
    lines = []
    
    # 4,300 normal HTTP access logs
    for i in range(1, 4301):
        curr_time += timedelta(milliseconds=random.randint(10, 50))
        tstr = curr_time.strftime("%d/%b/%Y:%H:%M:%S +0000")
        ip = f"10.244.{random.randint(1, 10)}.{random.randint(1, 254)}"
        dur = random.uniform(0.005, 0.045)
        path = random.choice(["/api/v1/search", "/api/v1/catalog", "/api/v1/user/profile", "/healthz"])
        lines.append(f'{ip} - - [{tstr}] "GET {path} HTTP/1.1" 200 {random.randint(300, 4500)} "-" "Mozilla/5.0" rt={dur:.3f} uct="0.001" uht="{dur:.3f}" urt="{dur:.3f}"')

    # Downstream database locks up, causing upstream 504 Gateway Timeouts
    for i in range(1, 701):
        curr_time += timedelta(milliseconds=random.randint(20, 80))
        tstr = curr_time.strftime("%d/%b/%Y:%H:%M:%S +0000")
        ip = f"10.244.{random.randint(1, 10)}.{random.randint(1, 254)}"
        
        if i % 3 == 0:
            lines.append(f'{curr_time.isoformat()}Z [error] 18#18: *{10000+i} upstream gateway timeout context deadline exceeded (110: Connection timed out) while reading response header from upstream, client: {ip}, server: gateway.internal, request: "POST /api/v1/checkout HTTP/1.1", upstream: "http://10.244.3.44:8080/checkout"')
            lines.append(f'{ip} - - [{tstr}] "POST /api/v1/checkout HTTP/1.1" 504 182 "-" "MobileApp/5.0" rt=30.002 uct="0.002" uht="30.000" urt="30.001"')
        else:
            lines.append(f'{ip} - - [{tstr}] "GET /api/v1/catalog HTTP/1.1" 200 {random.randint(300, 1500)} "-" "MobileApp/5.0" rt=0.015')

    with open(filepath, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"Generated {filepath} ({len(lines)} lines)")

if __name__ == "__main__":
    generate_payment_oom_log()
    generate_go_panic_log()
    generate_envoy_timeout_log()
