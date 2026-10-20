package example;
public class ExampleTest {
    @org.junit.Test public void works() { org.junit.Assert.assertEquals(4, 2 + 2); }
    @org.junit.Test @org.junit.Ignore("fixture") public void ignored() {}
}
