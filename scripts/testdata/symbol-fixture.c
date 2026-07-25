__attribute__((noinline))
int telemetry_fixture(int value) {
    return value + 42;
}

int main(void) {
    return telemetry_fixture(0);
}
