package example;
public class ExampleTest {
    @org.junit.jupiter.api.Test void works() { org.junit.jupiter.api.Assertions.assertEquals(4, 2 + 2); }
    @org.junit.jupiter.api.Test @org.junit.jupiter.api.Disabled("fixture") void ignored() {}
    @org.junit.jupiter.api.Test void injected(org.junit.jupiter.api.TestInfo info) {
        org.junit.jupiter.api.Assertions.assertTrue(info.getTestMethod().isPresent());
    }
}
